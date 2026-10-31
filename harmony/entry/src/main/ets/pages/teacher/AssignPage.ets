// 阅卷任务分配(管理员/年级组长):
//   无参数进入(☰ 导航)→ 先选考试;带 ?exam= → 直接进入。
//   阅卷人用芯片选择(替代 Select 组件,触屏更稳),按题/按班两种模式;截止时间随行保存。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { ExamDetail, ExamSummary } from '../../model/Api';
import { OgSession } from '../../common/OgSession';
import { EmptyView, ErrorView, LoadingView, PrimaryButton, SectionHeader, TitleBar } from '../../components/Ui';

interface AssignParams {
  exam: string;
}

interface AssignRow {
  key: string; // q{n} 或班级名
  label: string;
  max: number;
  teacher: string;
  due: string;
}

@Entry
@Component
struct AssignPage {
  @State examId: string = '';
  @State d: ExamDetail | null = null;
  @State rows: AssignRow[] = [];
  @State choices: ExamSummary[] = []; // 无参进入时的考试选择列表
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State busy: boolean = false;

  aboutToAppear(): void {
    const p = router.getParams() as AssignParams | null;
    if (p !== null && p !== undefined && p.exam !== undefined) {
      this.examId = p.exam;
    }
    this.load();
  }

  private async load(): Promise<void> {
    this.loading = this.rows.length === 0 && this.choices.length === 0;
    this.failed = false;
    try {
      if (this.examId === '') {
        const all = (await OgNet.jget<ExamSummary[]>('/api/v1/exams')) ?? [];
        const out: ExamSummary[] = [];
        for (const e of all) {
          if (e.subjective > 0 || e.stage === 'grading') {
            out.push(e);
          }
        }
        this.choices = out;
        this.rows = [];
      } else {
        await this.loadExam();
      }
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  private async loadExam(): Promise<void> {
    this.d = await OgNet.jget<ExamDetail>('/api/v1/exams/' + this.examId);
    const rows: AssignRow[] = [];
    if (this.d !== null) {
      if (this.d.taskMode === 'class') {
        for (const c of this.d.classes ?? []) {
          rows.push({
            key: c,
            label: c + ' 全科',
            max: 0,
            teacher: this.d.classTasks !== undefined ? (this.d.classTasks[c] ?? '') : '',
            due: this.d.taskDue !== undefined ? (this.d.taskDue[c] ?? '') : ''
          } as AssignRow);
        }
      } else {
        for (const prog of this.d.progress) {
          rows.push({
            key: 'q' + prog.question,
            label: '第 ' + (this.d.objective.length + prog.question) + ' 题(满分 ' + OgFormat.score(prog.max)
              + (prog.elective !== undefined && prog.elective !== '' ? ' · 选做' : '') + ')',
            max: prog.max,
            teacher: prog.teacher ?? '',
            due: this.d.taskDue !== undefined ? (this.d.taskDue['q' + prog.question] ?? '') : ''
          } as AssignRow);
        }
      }
    }
    this.rows = rows;
  }

  private canAssign(): boolean {
    return OgSession.isManager(); // 服务端 POST /tasks = manage(仅管理员/组长,建卷人也不行)
  }

  private async saveRow(row: AssignRow): Promise<void> {
    if (!this.canAssign()) {
      promptAction.showToast({ message: '仅管理员/年级组长可分配任务' });
      return;
    }
    this.busy = true;
    try {
      if (this.d !== null && this.d.taskMode === 'class') {
        await OgNet.jpost<Record<string, Object>>('/api/v1/exams/' + this.examId + '/tasks', {
          'class': row.key, 'teacher': row.teacher
        } as Record<string, Object>);
      } else {
        await OgNet.jpost<Record<string, Object>>('/api/v1/exams/' + this.examId + '/tasks', {
          'question': parseInt(row.key.substring(1), 10),
          'teacher': row.teacher
        } as Record<string, Object>);
      }
      if (row.due.trim() !== '') {
        await OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/task-due', {
          'key': row.key, 'due': row.due.trim()
        } as Record<string, string>);
      }
      promptAction.showToast({ message: row.label + ' 已保存' });
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '保存失败', duration: 3000 });
    }
    this.busy = false;
  }

  private staffNames(): string[] {
    if (this.d === null) {
      return [];
    }
    const out: string[] = [];
    for (const s of this.d.staff) {
      out.push(s.username);
    }
    return out;
  }

  private staffLabel(username: string): string {
    if (this.d === null) {
      return username;
    }
    for (const s of this.d.staff) {
      if (s.username === username) {
        return s.displayName;
      }
    }
    return username;
  }

  @Builder
  choiceList() {
    Column({ space: OgDim.GAP }) {
      SectionHeader({ text: '选择要分配的考试', extra: '仅列有主观题的考试' })
      if (this.choices.length === 0) {
        EmptyView({ text: '没有可分配的考试' })
      }
      ForEach(this.choices, (e: ExamSummary) => {
        Row({ space: 8 }) {
          Column({ space: 2 }) {
            Text((e.subject !== undefined && e.subject !== '') ? e.subject : '未标学科')
              .fontSize(14).fontWeight(FontWeight.Medium).fontColor(OgC.INK)
            Text(e.examDate !== undefined && e.examDate !== '' ? e.examDate : e.createdAt)
              .fontSize(11).fontColor(OgC.INK_SUB)
          }
          .alignItems(HorizontalAlign.Start)
          .layoutWeight(1)
          Text(e.stageLabel).fontSize(12).fontColor(OgC.BRAND)
          Text('›').fontSize(16).fontColor(OgC.INK_SUB)
        }
        .width('100%')
        .padding(12)
        .backgroundColor(OgC.CARD)
        .borderRadius(OgDim.RADIUS)
        .onClick(() => {
          this.examId = e.id;
          this.choices = [];
          this.load();
        })
      }, (e: ExamSummary) => e.id)
      Text('').height(16)
    }
    .padding(OgDim.PAD)
    .width('100%')
  }

  @Builder
  teacherChips(row: AssignRow) {
    Flex({ wrap: FlexWrap.Wrap }) {
      ForEach(this.staffNames(), (name: string) => {
        Text(this.staffLabel(name))
          .fontSize(12)
          .fontColor(row.teacher === name ? Color.White : OgC.INK)
          .backgroundColor(row.teacher === name ? OgC.BRAND : OgC.BG)
          .padding({ left: 10, right: 10, top: 5, bottom: 5 })
          .borderRadius(13)
          .margin({ right: 6, bottom: 6 })
          .onClick(() => {
            row.teacher = name;
            this.rows = this.rows.slice(0);
          })
      }, (name: string) => name + '-' + String(row.teacher === name))
      Text('不分配')
        .fontSize(12)
        .fontColor(row.teacher === '' ? Color.White : OgC.INK_SUB)
        .backgroundColor(row.teacher === '' ? OgC.INK_SUB : OgC.BG)
        .padding({ left: 10, right: 10, top: 5, bottom: 5 })
        .borderRadius(13)
        .margin({ right: 6, bottom: 6 })
        .onClick(() => {
          row.teacher = '';
          this.rows = this.rows.slice(0);
        })
    }
    .width('100%')
  }

  @Builder
  assignCard(row: AssignRow) {
    Column({ space: 8 }) {
      Row({ space: 8 }) {
        Text(row.label).fontSize(13).fontWeight(FontWeight.Medium).fontColor(OgC.INK).layoutWeight(1)
        if (row.teacher !== '') {
          Text('阅卷人 ' + this.staffLabel(row.teacher)).fontSize(11).fontColor(OgC.BRAND)
        }
      }
      .width('100%')
      this.teacherChips(row)
      if (this.canAssign()) {
        Row({ space: 8 }) {
          TextInput({ text: row.due, placeholder: '截止 YYYY-MM-DD(可选)' })
            .fontSize(12).height(36).layoutWeight(1)
            .onChange((v: string) => {
              row.due = v;
            })
          PrimaryButton({
            label: this.busy ? '…' : '保存',
            usable: !this.busy,
            onTap: () => this.saveRow(row)
          })
        }
        .width('100%')
      } else {
        Text('仅管理员/年级组长可分配任务,此处为只读视图。')
          .fontSize(11).fontColor(OgC.INK_SUB)
      }
    }
    .padding(12)
    .margin({ left: OgDim.PAD, right: OgDim.PAD })
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .width('100%')
    .alignItems(HorizontalAlign.Start)
  }

  build() {
    Column() {
      TitleBar({
        title: '任务分配',
        subtitle: this.examId === '' ? '选择考试' : this.examId + (this.d !== null && this.d.taskMode === 'class' ? ' · 按班模式' : ''),
        onBack: () => router.back()
      })
      if (this.loading) {
        LoadingView()
      } else if (this.failed) {
        ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
      } else if (this.examId === '') {
        Scroll() {
          this.choiceList()
        }
        .layoutWeight(1)
        .scrollBar(BarState.Off)
        .align(Alignment.Top)
      } else if (this.rows.length === 0) {
        EmptyView({ text: '该考试没有可分配的任务(无主观题且非按班模式)' })
      } else {
        Column({ space: 8 }) {
          Text('点芯片分配阅卷人,「不分配」清除;保存时同步提交截止时间。')
            .fontSize(11).fontColor(OgC.INK_SUB)
            .padding({ left: OgDim.PAD, right: OgDim.PAD, top: 8 })
        }
        .width('100%')
        List({ space: 8 }) {
          ForEach(this.rows, (row: AssignRow) => {
            ListItem() {
              this.assignCard(row)
            }
          }, (row: AssignRow) => row.key + '-' + row.teacher + '-' + row.due)
        }
        .layoutWeight(1)
        .width('100%')
        .scrollBar(BarState.Off)
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
  }
}
