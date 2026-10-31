// 待办中心(教职员):聚合既有端点的待办事项 —— 阅卷任务(按题/按班/建卷兜底)、
// 异常待裁决、⚑ 待复核、复核申请、待发布考试;紧急项置顶。零新服务端接口。
// 附:任务截止本地提醒(reminderAgentManager,纯本地,无需云推送)。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { reminderAgentManager } from '@kit.BackgroundTasksKit';
import { notificationManager } from '@kit.NotificationKit';
import { common } from '@kit.AbilityKit';
import { OgNet, OgError } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgBreakpoint } from '../../common/OgBreakpoint';

import { OgFormat } from '../../common/OgFormat';
import { ExamSummary, TaskItem } from '../../model/Api';
import { EmptyView, ErrorView, LoadingView, Pill, PrimaryButton, SectionHeader, TitleBar } from '../../components/Ui';
import { StaffNavGrid, navSheetOptions } from '../../components/StaffNav';

interface TodoItem {
  key: string;
  kind: string; // task | anomaly | flag | appeal | publish
  label: string;
  detail: string;
  urgent: boolean;
  done: boolean;
  exam: string;
  question: number; // 0 = 非按题任务
  due: string;
}

@Entry
@Component
struct TodoPage {
  @State items: TodoItem[] = [];
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State navOpen: boolean = false;
  @State remindBusy: boolean = false;
  @State remindMsg: string = '';

  aboutToAppear(): void {
    this.load();
  }

  private async load(): Promise<void> {
    this.loading = this.items.length === 0;
    this.failed = false;
    try {
      let tasks: TaskItem[] = [];
      let exams: ExamSummary[] = [];
      try {
        tasks = (await OgNet.jget<TaskItem[]>('/api/v1/my/tasks')) ?? [];
      } catch (e) {
        tasks = []; // 兜底角色(纯管理员)无任务接口
      }
      exams = (await OgNet.jget<ExamSummary[]>('/api/v1/exams')) ?? [];
      this.items = this.aggregate(tasks, exams);
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  private aggregate(tasks: TaskItem[], exams: ExamSummary[]): TodoItem[] {
    const out: TodoItem[] = [];
    const now = Date.now();
    for (const t of tasks) {
      const done = t.total > 0 && t.graded >= t.total;
      const urgent = !done && this.dueSoon(t.due ?? '', now);
      out.push({
        key: 'task-' + t.exam + '-' + t.kind + '-' + (t.question ?? 0),
        kind: 'task',
        label: done ? '已阅完:' + t.label : '阅卷:' + t.label,
        detail: t.subject !== undefined && t.subject !== '' ? t.subject + ' · ' + t.graded + '/' + t.total
          + (t.due !== undefined && t.due !== '' ? ' · 截止 ' + t.due : '') : '',
        urgent: urgent,
        done: done,
        exam: t.exam,
        question: t.question ?? 0,
        due: t.due ?? ''
      });
    }
    for (const e of exams) {
      const subj = e.subject !== undefined && e.subject !== '' ? e.subject : e.id;
      if (e.stage === 'anomalies' && e.warnings > 0) {
        out.push({
          key: 'anom-' + e.id, kind: 'anomaly',
          label: '异常卷待裁决:' + subj,
          detail: e.warnings + ' 卷待处理(推荐考号/缺考/作废)',
          urgent: true, done: false, exam: e.id, question: 0, due: ''
        });
      }
      if ((e.flagged ?? 0) > 0) {
        out.push({
          key: 'flag-' + e.id, kind: 'flag',
          label: '⚑ 待复核:' + subj,
          detail: (e.flagged ?? 0) + ' 份待复核改分',
          urgent: false, done: false, exam: e.id, question: 0, due: ''
        });
      }
      if ((e.appeals ?? 0) > 0) {
        out.push({
          key: 'appeal-' + e.id, kind: 'appeal',
          label: '成绩复核申请:' + subj,
          detail: (e.appeals ?? 0) + ' 条待回复裁决',
          urgent: true, done: false, exam: e.id, question: 0, due: ''
        });
      }
      if (e.stage === 'ready' && e.mine) {
        out.push({
          key: 'pub-' + e.id, kind: 'publish',
          label: '待发布:' + subj,
          detail: '阅卷完成,核对后发布成绩',
          urgent: false, done: false, exam: e.id, question: 0, due: ''
        });
      }
    }
    // 紧急在前,同组已完成沉底
    out.sort((a, b) => {
      if (a.done !== b.done) {
        return a.done ? 1 : -1;
      }
      if (a.urgent !== b.urgent) {
        return a.urgent ? -1 : 1;
      }
      return 0;
    });
    return out;
  }

  private dueSoon(due: string, now: number): boolean {
    if (due === undefined || due === '') {
      return false;
    }
    const t = Date.parse(due.replace(' ', 'T'));
    if (isNaN(t)) {
      return false;
    }
    return t - now < 48 * 3600 * 1000;
  }

  private open(it: TodoItem): void {
    if (it.kind === 'task' && it.question > 0 && !it.done) {
      router.pushUrl({
        url: 'pages/teacher/GraderPage',
        params: { 'exam': it.exam, 'q': String(it.question) } as Record<string, string>
      }).catch(() => {});
      return;
    }
    router.pushUrl({
      url: 'pages/teacher/ExamHubPage',
      params: { 'exam': it.exam } as Record<string, string>
    }).catch(() => {});
  }

  // —— 截止本地提醒 ——

  private pendingDues(): TodoItem[] {
    const out: TodoItem[] = [];
    for (const it of this.items) {
      if (it.kind === 'task' && !it.done && it.due !== '') {
        out.push(it);
      }
    }
    return out;
  }

  // 为所有未完成且带截止的任务发布日历型本地提醒(截止前 2 小时;重复点按先撤旧再发新)。
  private async scheduleReminders(): Promise<void> {
    const dues = this.pendingDues();
    if (dues.length === 0) {
      promptAction.showToast({ message: '没有带截止时间的未完成任务' });
      return;
    }
    this.remindBusy = true;
    try {
      await notificationManager.requestEnableNotification(getContext(this) as common.UIAbilityContext);
    } catch (e) {
      promptAction.showToast({ message: '未获得通知授权,提醒无法送达', duration: 3000 });
      this.remindBusy = false;
      return;
    }
    // 撤销旧提醒(尽力而为)
    try {
      const olds = await OgSession.getJson('todoReminders');
      if (olds !== '') {
        const arr = JSON.parse(olds) as number[];
        for (const id of arr) {
          try {
            await reminderAgentManager.cancelReminder(id);
          } catch (e) {
            // 已过期/不存在忽略
          }
        }
      }
    } catch (e) {
      // 忽略
    }
    const ids: number[] = [];
    let skipped = 0;
    for (const it of dues) {
      const at = this.remindTime(it.due);
      if (at === null) {
        skipped++;
        continue;
      }
      try {
        const reminder: reminderAgentManager.ReminderRequestCalendar = {
          reminderType: reminderAgentManager.ReminderType.REMINDER_TYPE_CALENDAR,
          dateTime: at,
          title: '阅卷任务临近截止',
          content: it.label + '(截止 ' + it.due + ')',
          expiredContent: '该任务截止提醒已过期'
        };
        const id = await reminderAgentManager.publishReminder(reminder);
        ids.push(id);
      } catch (e) {
        skipped++;
      }
    }
    await OgSession.setJson('todoReminders', JSON.stringify(ids));
    this.remindMsg = '已设 ' + ids.length + ' 条截止提醒(提前 2 小时)' + (skipped > 0 ? ',跳过 ' + skipped + ' 条(时间过近或无效)' : '');
    promptAction.showToast({ message: this.remindMsg, duration: 3500 });
    this.remindBusy = false;
  }

  // 截止前 2 小时;截止已不足 2 小时则不提醒(避免过期提醒轰炸)。
  private remindTime(due: string): reminderAgentManager.LocalDateTime | null {
    const t = Date.parse(due.replace(' ', 'T'));
    if (isNaN(t)) {
      return null;
    }
    const at = new Date(t - 2 * 3600 * 1000);
    if (at.getTime() <= Date.now()) {
      return null;
    }
    const out: reminderAgentManager.LocalDateTime = {
      year: at.getFullYear(),
      month: at.getMonth() + 1,
      day: at.getDate(),
      hour: at.getHours(),
      minute: at.getMinutes(),
      second: 0
    };
    return out;
  }

  private pendingCount(): number {
    let n = 0;
    for (const it of this.items) {
      if (!it.done) {
        n++;
      }
    }
    return n;
  }

  private urgentCount(): number {
    let n = 0;
    for (const it of this.items) {
      if (!it.done && it.urgent) {
        n++;
      }
    }
    return n;
  }

  @Builder
  navAction() {
    Text('☰').fontSize(20).fontColor(OgC.BRAND).padding({ left: 10, right: 4 })
      .onClick(() => {
        this.navOpen = true;
      })
  }

  @Builder
  todoRow(it: TodoItem) {
    Row({ space: 10 }) {
      Column({ space: 3 }) {
        Row({ space: 6 }) {
          Text(it.label)
            .fontSize(14)
            .fontWeight(it.urgent ? FontWeight.Bold : FontWeight.Medium)
            .fontColor(it.done ? OgC.INK_SUB : OgC.INK)
            .layoutWeight(1)
            .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
          if (it.urgent) {
            Pill({ text: '紧急', color: OgC.BAD })
          }
          if (it.done) {
            Pill({ text: '已完成', color: OgC.INK_SUB })
          }
        }
        .width('100%')
        if (it.detail !== '') {
          Text(it.detail).fontSize(11).fontColor(OgC.INK_SUB)
        }
      }
      .alignItems(HorizontalAlign.Start)
      .layoutWeight(1)
      Text('›').fontSize(16).fontColor(OgC.INK_SUB)
    }
    .width('100%')
    .padding({ top: 9, bottom: 9 })
    .border({ width: { bottom: 1 }, color: OgC.LINE })
    .onClick(() => this.open(it))
  }

  build() {
    Column() {
      TitleBar({
        title: '待办中心',
        subtitle: '任务 / 异常 / 复核 / 发布 一站聚合',
        showBack: false,
        actions: (): void => {
          this.navAction()
        }
      })
      if (this.loading) {
        LoadingView()
      } else if (this.failed) {
        ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
      } else {
        Column({ space: 8 }) {
          Row({ space: 10 }) {
            SectionHeader({
              text: '待处理 ' + this.pendingCount() + ' 项',
              extra: this.urgentCount() > 0 ? '紧急 ' + this.urgentCount() : ''
            })
          }
          .width('100%')
          PrimaryButton({
            full: true,
            usable: !this.remindBusy,
            label: this.remindBusy ? '设置提醒中…' : '🔔 为阅卷任务开截止提醒(提前 2 小时)',
            onTap: () => this.scheduleReminders()
          })
          if (this.remindMsg !== '') {
            Text(this.remindMsg).fontSize(11).fontColor(OgC.GOOD)
          }
        }
        .padding({ left: OgDim.PAD, right: OgDim.PAD, top: 8 })
        .width('100%')
        .alignItems(HorizontalAlign.Start)
        Scroll() {
          Column({ space: 2 }) {
            if (this.items.length === 0) {
              EmptyView({ text: '太棒了,没有任何待办!' })
            }
            ForEach(this.items, (it: TodoItem) => {
              this.todoRow(it)
            }, (it: TodoItem) => it.key + '-' + String(it.done) + String(it.urgent))
            Text('数据聚合自「我的任务 + 考试列表」,下拉重新进入即刷新。')
              .fontSize(10).fontColor(OgC.INK_SUB).margin({ top: 10 })
          }
          .padding({ left: OgDim.PAD, right: OgDim.PAD, bottom: 20 })
          .width('100%')
          .constraintSize({ maxWidth: OgDim.MAXW })
        }
        .layoutWeight(1)
        .scrollBar(BarState.Off)
        .align(Alignment.Top)
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
    .bindSheet($$this.navOpen, this.sheetBuilder(), navSheetOptions(OgBreakpoint.now(), '工作台导航'))
  }

  @Builder
  sheetBuilder() {
    StaffNavGrid({ current: 'todo', onClose: () => {
      this.navOpen = false;
    } })
  }
}
