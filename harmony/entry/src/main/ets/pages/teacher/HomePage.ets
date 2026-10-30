// 工作台总览(教职员首页):任务与考试的聚合仪表盘 + 高频入口 + 最近考试。
// 数据全部来自既有端点(/api/v1/my/tasks + /api/v1/exams),服务端零改动。

import { router } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { ExamSummary, TaskItem } from '../../model/Api';
import { EmptyView, ErrorView, LoadingView, Pill, TitleBar } from '../../components/Ui';
import { StaffNavGrid } from '../../components/StaffNav';

interface QuickEntry {
  label: string;
  url: string;
}

const QUICK: QuickEntry[] = [
  { label: '📋 我的任务', url: 'pages/teacher/TaskListPage' },
  { label: '📝 考试列表', url: 'pages/teacher/ExamListPage' },
  { label: '➕ 新建考试', url: 'pages/teacher/CreateExamPage' },
  { label: '👥 任务分配', url: 'pages/teacher/AssignPage' },
  { label: '📈 纵向趋势', url: 'pages/teacher/StaffTrendsPage' },
  { label: '📊 年级总表', url: 'pages/teacher/AggregatePage' },
  { label: '📚 校本题库', url: 'pages/teacher/BankListPage' },
  { label: '⚙️ 账号设置', url: 'pages/MePage' }
];

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
      this.exams = await OgNet.jget<ExamSummary[]>('/api/v1/exams');
      try {
        this.tasks = await OgNet.jget<TaskItem[]>('/api/v1/my/tasks');
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
      if (q.label === '👥 任务分配' && !OgSession.isManager()) {
        continue; // 服务端 POST /tasks 仅管理员/组长
      }
      out.push(q);
    }
    return out;
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
  statTile(v: number, label: string, color: string, onTap: () => void) {
    Column({ space: 2 }) {
      Text(String(v)).fontSize(22).fontWeight(FontWeight.Bold).fontColor(color)
      Text(label).fontSize(11).fontColor(OgC.INK_SUB)
        .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
    }
    .layoutWeight(1)
    .padding({ top: 10, bottom: 10 })
    .backgroundColor(OgC.CARD)
    .borderRadius(10)
    .onClick(() => onTap())
  }

  @Builder
  quickCell(entry: QuickEntry) {
    Text(entry.label)
      .fontSize(13)
      .fontColor(OgC.INK)
      .backgroundColor(OgC.CARD)
      .borderRadius(10)
      .padding({ left: 14, right: 14, top: 10, bottom: 10 })
      .margin({ right: 8, bottom: 8 })
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
              // 快捷入口
              Flex({ wrap: FlexWrap.Wrap }) {
                ForEach(this.quickEntries(), (entry: QuickEntry) => {
                  this.quickCell(entry)
                }, (entry: QuickEntry) => entry.url)
              }
              .width('100%')

              // 任务概况
              Column({ space: 8 }) {
                Row({ space: 8 }) {
                  this.statTile(this.tasks.length, '我的任务', OgC.INK, () => {
                    router.pushUrl({ url: 'pages/teacher/TaskListPage' }).catch(() => {});
                  })
                  this.statTile(this.undoneTasks(), '未完成任务', this.undoneTasks() > 0 ? OgC.WARN : OgC.INK_SUB, () => {
                    router.pushUrl({ url: 'pages/teacher/TaskListPage' }).catch(() => {});
                  })
                }
                .width('100%')
                Row({ space: 8 }) {
                  this.statTile(this.stageCount(['awaiting_scan', 'scanning', 'anomalies', 'grading']),
                    '进行中考试', OgC.BRAND, () => {
                      router.pushUrl({ url: 'pages/teacher/ExamListPage' }).catch(() => {});
                    })
                  this.statTile(this.stageCount(['ready']), '待发布', OgC.WARN, () => {
                    router.pushUrl({ url: 'pages/teacher/ExamListPage' }).catch(() => {});
                  })
                  this.statTile(this.stageCount(['published']), '已发布', OgC.INK_SUB, () => {
                    router.pushUrl({ url: 'pages/teacher/ExamListPage' }).catch(() => {});
                  })
                }
                .width('100%')
                Row({ space: 8 }) {
                  this.statTile(this.sumOf((e: ExamSummary) => e.ungraded), '待阅切片', OgC.WARN, () => {
                    router.pushUrl({ url: 'pages/teacher/TaskListPage' }).catch(() => {});
                  })
                  this.statTile(this.sumOf((e: ExamSummary) => e.warnings), '待处理异常', OgC.BAD, () => {
                    router.pushUrl({ url: 'pages/teacher/ExamListPage' }).catch(() => {});
                  })
                  this.statTile(this.sumOf((e: ExamSummary) => (e.appeals ?? 0)), '复核申请', OgC.FLAG, () => {
                    router.pushUrl({ url: 'pages/teacher/ExamListPage' }).catch(() => {});
                  })
                }
                .width('100%')
              }
              .width('100%')

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
    .bindSheet($$this.navOpen, this.sheetBuilder(), {
      height: 360, dragBar: true, title: { title: '工作台导航' }
    })
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
