// 工作台总览(教职员首页):任务与考试的聚合仪表盘 + 高频入口 + 最近考试。
// 数据全部来自既有端点(/api/v1/my/tasks + /api/v1/exams),服务端零改动。
// 自适应:快捷入口/统计用 GridRow 官方栅格,列数按断点配置(外屏窄竖屏 2×2,内屏/横屏一行)。

import { router } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgBreakpoint } from '../../common/OgBreakpoint';

import { OgFormat } from '../../common/OgFormat';
import { ExamSummary, TaskItem } from '../../model/Api';
import { EmptyView, ErrorView, LoadingView, Pill, TitleBar } from '../../components/Ui';
import { StaffNavGrid, navSheetOptions } from '../../components/StaffNav';

interface QuickEntry {
  icon: string;
  label: string;
  url: string;
}

const QUICK: QuickEntry[] = [
  { icon: '🔔', label: '待办中心', url: 'pages/teacher/TodoPage' },
  { icon: '📋', label: '我的任务', url: 'pages/teacher/TaskListPage' },
  { icon: '📷', label: '拍卷建考', url: 'pages/teacher/LayoutScanPage' },
  { icon: '📝', label: '考试列表', url: 'pages/teacher/ExamListPage' },
  { icon: '➕', label: '新建考试', url: 'pages/teacher/CreateExamPage' },
  { icon: '👥', label: '任务分配', url: 'pages/teacher/AssignPage' },
  { icon: '📈', label: '纵向趋势', url: 'pages/teacher/StaffTrendsPage' },
  { icon: '📊', label: '年级总表', url: 'pages/teacher/AggregatePage' },
  { icon: '📚', label: '校本题库', url: 'pages/teacher/BankListPage' },
  { icon: '⚙️', label: '账号设置', url: 'pages/MePage' }
];

// 统计项:路由用 url 字符串(点按仅 pushUrl),避免 @Builder 闭包参数
interface StatItem {
  n: number;
  label: string;
  color: string;
  url: string;
}

@Entry
@Component
struct HomePage {
  @State exams: ExamSummary[] = [];
  @State tasks: TaskItem[] = [];
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State navOpen: boolean = false;

  aboutToAppear(): void {
    this.load();
  }

  private async load(): Promise<void> {
    this.loading = this.exams.length === 0 && this.tasks.length === 0;
    this.failed = false;
    try {
      this.exams = (await OgNet.jget<ExamSummary[]>('/api/v1/exams')) ?? [];
      try {
        this.tasks = (await OgNet.jget<TaskItem[]>('/api/v1/my/tasks')) ?? [];
      } catch (e) {
        this.tasks = []; // 任务接口对学生角色不可达,总览容忍
      }
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
    this.refreshingFlag = false;
  }

  private stageCount(stages: string[]): number {
    let n = 0;
    for (const e of this.exams) {
      if (stages.indexOf(e.stage) >= 0) {
        n++;
      }
    }
    return n;
  }

  private sumOf(pick: (e: ExamSummary) => number): number {
    let n = 0;
    for (const e of this.exams) {
      n += pick(e);
    }
    return n;
  }

  private undoneTasks(): number {
    let n = 0;
    for (const t of this.tasks) {
      if (t.total > 0 && t.graded < t.total) {
        n++;
      }
    }
    return n;
  }

  private quickEntries(): QuickEntry[] {
    const out: QuickEntry[] = [];
    for (const q of QUICK) {
      if (q.label === '任务分配' && !OgSession.isManager()) {
        continue; // 服务端 POST /tasks 仅管理员/组长
      }
      out.push(q);
    }
    return out;
  }

  // 主指标 4 项:窄屏(折叠外屏)2×2,内屏/横屏一行四个
  private primaryStats(): StatItem[] {
    return [
      { n: this.tasks.length, label: '我的任务', color: OgC.INK, url: 'pages/teacher/TaskListPage' },
      {
        n: this.undoneTasks(), label: '未完成任务',
        color: this.undoneTasks() > 0 ? OgC.WARN : OgC.INK_SUB, url: 'pages/teacher/TaskListPage'
      },
      {
        n: this.stageCount(['awaiting_scan', 'scanning', 'anomalies', 'grading']),
        label: '进行中考试', color: OgC.BRAND, url: 'pages/teacher/ExamListPage'
      },
      { n: this.sumOf((e: ExamSummary) => e.ungraded), label: '待阅切片', color: OgC.WARN, url: 'pages/teacher/TaskListPage' }
    ];
  }

  // 次要指标 4 项:统一一行小字
  private secondaryStats(): StatItem[] {
    return [
      { n: this.stageCount(['ready']), label: '待发布', color: OgC.WARN, url: 'pages/teacher/ExamListPage' },
      { n: this.stageCount(['published']), label: '已发布', color: OgC.GOOD, url: 'pages/teacher/ExamListPage' },
      { n: this.sumOf((e: ExamSummary) => e.warnings), label: '异常', color: OgC.BAD, url: 'pages/teacher/ExamListPage' },
      { n: this.sumOf((e: ExamSummary) => (e.appeals ?? 0)), label: '复核', color: OgC.FLAG, url: 'pages/teacher/ExamListPage' }
    ];
  }

  private recentExams(): ExamSummary[] {
    const out: ExamSummary[] = [];
    const n = Math.min(5, this.exams.length);
    for (let i = 0; i < n; i++) {
      out.push(this.exams[i]);
    }
    return out;
  }

  @Builder
  statCell(st: StatItem) {
    Column({ space: 2 }) {
      Text(String(st.n)).fontSize(21).fontWeight(FontWeight.Bold).fontColor(st.color)
      Text(st.label).fontSize(10).fontColor(OgC.INK_SUB)
        .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
    }
    .width('100%')
    .padding({ top: 11, bottom: 11 })
    .onClick(() => {
      router.pushUrl({ url: st.url }).catch(() => {});
    })
  }

  @Builder
  miniStat(st: StatItem) {
    Row({ space: 4 }) {
      Text(st.label).fontSize(11).fontColor(OgC.INK_SUB).maxLines(1)
      Text(String(st.n)).fontSize(12).fontWeight(FontWeight.Bold).fontColor(st.color)
    }
    .width('100%')
    .justifyContent(FlexAlign.Center)
    .padding({ top: 6, bottom: 6 })
    .onClick(() => {
      router.pushUrl({ url: st.url }).catch(() => {});
    })
  }

  @Builder
  quickCell(entry: QuickEntry) {
    Column({ space: 3 }) {
      Text(entry.icon).fontSize(20)
      Text(entry.label).fontSize(11).fontColor(OgC.INK)
        .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
    }
    .width('100%')
    .height(64)
    .justifyContent(FlexAlign.Center)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .onClick(() => {
      router.pushUrl({ url: entry.url }).catch(() => {});
    })
  }

  @Builder
  recentRow(e: ExamSummary) {
    Row({ space: 8 }) {
      Column({ space: 2 }) {
        Text((e.subject !== undefined && e.subject !== '') ? e.subject : '未标学科')
          .fontSize(14).fontWeight(FontWeight.Medium).fontColor(OgC.INK)
        Text(e.examDate !== undefined && e.examDate !== '' ? e.examDate : e.createdAt)
          .fontSize(11).fontColor(OgC.INK_SUB)
      }
      .alignItems(HorizontalAlign.Start)
      .layoutWeight(1)
      Column({ space: 3 }) {
        Pill({ text: e.stageLabel, color: e.published ? OgC.GOOD : (e.stage === 'anomalies' ? OgC.BAD : OgC.WARN) })
        if (e.ungraded > 0) {
          Text('未阅 ' + e.ungraded).fontSize(10).fontColor(OgC.WARN)
        }
      }
      .alignItems(HorizontalAlign.End)
      Text('›').fontSize(16).fontColor(OgC.INK_SUB)
    }
    .width('100%')
    .padding({ top: 9, bottom: 9 })
    .border({ width: { bottom: 1 }, color: OgC.LINE })
    .onClick(() => {
      router.pushUrl({
        url: 'pages/teacher/ExamHubPage', params: { 'exam': e.id } as Record<string, Object>
      }).catch(() => {});
    })
  }

  build() {
    Column() {
      TitleBar({
        title: '工作台',
        subtitle: OgSession.me() !== null
          ? OgSession.me()!.displayName + ' · ' + OgFormat.roleName(OgSession.me()!.role)
          : '',
        showBack: false,
        actions: (): void => {
          this.navAction()
        }
      })
      Refresh({ refreshing: this.refreshingFlag }) {
        Scroll() {
          Column({ space: OgDim.GAP }) {
            if (this.loading) {
              LoadingView()
            } else if (this.failed) {
              ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
            } else {
              // 快捷入口:图标网格(外屏 4 列,内屏/横屏 5 列)
              GridRow({ columns: { sm: 4, md: 5, lg: 5 }, gutter: 8 }) {
                ForEach(this.quickEntries(), (entry: QuickEntry) => {
                  GridCol({ span: 1 }) {
                    this.quickCell(entry)
                  }
                }, (entry: QuickEntry) => entry.url)
              }
              .width('100%')

              // 任务与考试概况:一张卡,主指标(窄屏 2×2/宽屏一行)+ 细分小字行
              Column() {
                GridRow({ columns: { sm: 2, md: 4, lg: 4 } }) {
                  ForEach(this.primaryStats(), (st: StatItem) => {
                    GridCol({ span: 1 }) {
                      this.statCell(st)
                    }
                  }, (st: StatItem) => st.label)
                }
                .width('100%')
                Row().height(1).width('100%').backgroundColor(OgC.LINE).margin({ top: 8 })
                GridRow({ columns: 4 }) {
                  ForEach(this.secondaryStats(), (st: StatItem) => {
                    GridCol({ span: 1 }) {
                      this.miniStat(st)
                    }
                  }, (st: StatItem) => st.label)
                }
                .width('100%')
              }
              .width('100%')
              .padding({ left: 4, right: 4 })
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)

              // 最近考试
              if (this.exams.length === 0) {
                EmptyView({ text: '还没有考试,点「新建考试」开始' })
              } else {
                Column({ space: 4 }) {
                  Text('最近考试')
                    .fontSize(13).fontWeight(FontWeight.Bold).fontColor(OgC.INK)
                    .margin({ top: 4, bottom: 2 })
                  ForEach(this.recentExams(), (e: ExamSummary) => {
                    this.recentRow(e)
                  }, (e: ExamSummary) => e.id + e.stage + String(e.published))
                  Text('共 ' + this.exams.length + ' 场 · 点右上角 ☰ 查看全部入口')
                    .fontSize(10).fontColor(OgC.INK_SUB)
                    .margin({ top: 6 })
                }
                .width('100%')
                .padding(14)
                .backgroundColor(OgC.CARD)
                .borderRadius(OgDim.RADIUS)
                .alignItems(HorizontalAlign.Start)
              }
              Text('').height(20)
            }
          }
          .padding(OgDim.PAD)
          .width('100%')
          .constraintSize({ maxWidth: OgDim.MAXW })
        }
        .scrollBar(BarState.Off)
        .align(Alignment.Top)
      }
      .onRefreshing(() => {
        this.refreshingFlag = true;
        this.load();
      })
      .layoutWeight(1)
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
    .bindSheet($$this.navOpen, this.sheetBuilder(), navSheetOptions(OgBreakpoint.now(), '工作台导航'))
  }

  @State refreshingFlag: boolean = false;

  private refreshing(): boolean {
    return this.refreshingFlag;
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
    StaffNavGrid({ current: 'home', onClose: () => {
      this.navOpen = false;
    } })
  }
}
