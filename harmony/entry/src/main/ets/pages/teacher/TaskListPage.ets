// 我的任务(教师登录首页):按题/按班/建卷人兜底三类任务,带进度与截止。
// 点按直达该题阅卷(GraderPage 深链)或考试详情。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgBreakpoint } from '../../common/OgBreakpoint';

import { OgFormat } from '../../common/OgFormat';
import { TaskItem } from '../../model/Api';
import { EmptyView, ErrorView, LoadingView, Pill, TitleBar } from '../../components/Ui';
import { StaffNavGrid, navSheetOptions } from '../../components/StaffNav';

@Entry
@Component
struct TaskListPage {
  @State items: TaskItem[] = [];
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State navOpen: boolean = false;
  @StorageProp('ogBp') bp: string = 'sm'; // md/lg 宽屏切双列卡片

  aboutToAppear(): void {
    this.load();
  }

  private async load(): Promise<void> {
    this.loading = this.items.length === 0;
    this.failed = false;
    try {
      this.items = (await OgNet.jget<TaskItem[]>('/api/v1/my/tasks')) ?? [];
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  private isDone(it: TaskItem): boolean {
    return it.total > 0 && it.graded >= it.total;
  }

  // 宽屏(md/lg)双列:任务卡对半分行,内屏/横屏上缩短扫视距离
  private cardRows(): TaskItem[][] {
    const rows: TaskItem[][] = [];
    for (let i = 0; i < this.items.length; i += 2) {
      rows.push(this.items.slice(i, i + 2));
    }
    return rows;
  }

  private openTask(it: TaskItem): void {
    if (it.kind === 'question' && this.isDone(it)) {
      promptAction.showToast({ message: '该任务已全部阅完(' + it.graded + '/' + it.total + ')' });
      return;
    }
    if (it.kind === 'question' && it.question !== undefined) {
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

  @Builder
  taskCard(it: TaskItem) {
    Column({ space: 6 }) {
      Row({ space: 8 }) {
        Text(it.label)
          .fontSize(15).fontWeight(FontWeight.Bold).fontColor(OgC.INK).layoutWeight(1)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        if (this.isDone(it)) {
          Pill({ text: '已完成', color: OgC.INK_SUB })
        }
        Pill({ text: it.stageLabel, color: it.stage === 'grading' || it.stage === 'ready' ? OgC.BRAND : OgC.WARN })
      }
      .width('100%')
      Row({ space: 8 }) {
        Text(it.subject !== undefined && it.subject !== '' ? it.subject : '未标学科')
          .fontSize(12).fontColor(OgC.INK_SUB)
        Text(it.exam).fontSize(11).fontColor(OgC.INK_SUB)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        Blank()
        if (it.due !== undefined && it.due !== '') {
          Text('截止 ' + it.due).fontSize(11).fontColor(OgC.BAD)
        }
      }
      .width('100%')
      Row({ space: 8 }) {
        Progress({ value: it.graded, total: Math.max(1, it.total), type: ProgressType.Linear })
          .color(OgC.BRAND)
          .layoutWeight(1)
          .height(10)
        Text(it.graded + '/' + it.total).fontSize(11).fontColor(OgC.INK_SUB)
        Text(it.kind === 'question' ? '去阅卷 ›' : '查看详情 ›').fontSize(12).fontColor(OgC.BRAND)
      }
      .width('100%')
    }
    .width('100%')
    .padding(14)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .onClick(() => this.openTask(it))
  }

  build() {
    Column() {
      TitleBar({
        title: '我的任务',
        showBack: false,
        actions: (): void => {
          this.navAction()
        }
      })
      Scroll() {
        Column({ space: OgDim.GAP }) {
          if (this.loading) {
            LoadingView()
          } else if (this.failed) {
            ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
          } else if (this.items.length === 0) {
            EmptyView({ text: '暂无阅卷任务。被分配任务或自建考试后会出现在这里。' })
          }
          if (this.bp !== 'sm') {
            ForEach(this.cardRows(), (row: TaskItem[], ri: number) => {
              Row({ space: OgDim.GAP }) {
                ForEach(row, (it: TaskItem) => {
                  Column() {
                    this.taskCard(it)
                  }
                  .layoutWeight(1)
                }, (it: TaskItem) => it.exam + '-' + it.kind + '-' + (it.question ?? 0))
              }
              .width('100%')
              .alignItems(VerticalAlign.Top)
            }, (row: TaskItem[], ri: number) => 'r' + ri + (row.length > 0 ? row[0].exam + row[0].kind : ''))
          } else {
            ForEach(this.items, (it: TaskItem) => {
              this.taskCard(it)
            }, (it: TaskItem) => it.exam + '-' + it.kind + '-' + (it.question ?? 0))
          }
          Text('').height(20)
        }
        .padding(OgDim.PAD)
        .width('100%')
        .constraintSize({ maxWidth: OgDim.MAXW })
      }
      .layoutWeight(1)
      .scrollBar(BarState.Off)
      .align(Alignment.Top)
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
    .bindSheet($$this.navOpen, this.sheetBuilder(), navSheetOptions(OgBreakpoint.now(), '工作台导航'))
  }

  @Builder
  navAction() {
    Text('☰')
      .fontSize(20).fontColor(OgC.BRAND).padding({ left: 10, right: 4 })
      .onClick(() => {
        this.navOpen = true;
      })
  }

  @Builder
  sheetBuilder() {
    StaffNavGrid({ current: 'tasks', onClose: () => {
      this.navOpen = false;
    } })
  }
}
