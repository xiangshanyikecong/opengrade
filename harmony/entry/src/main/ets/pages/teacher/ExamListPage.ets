// 考试列表:阶段状态机驱动主操作(去扫描/处理异常/阅卷/发布/成绩册),支持搜索与阶段过滤。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { ExamSummary } from '../../model/Api';
import { EmptyView, ErrorView, GhostButton, LoadingView, Pill, PrimaryButton, TitleBar } from '../../components/Ui';
import { StaffNavGrid } from '../../components/StaffNav';

const STAGE_FILTERS: string[] = ['', 'awaiting_scan', 'scanning', 'anomalies', 'grading', 'ready', 'published'];
const STAGE_NAMES: Record<string, string> = {
  '': '全部', 'awaiting_scan': '待扫描', 'scanning': '扫描中', 'anomalies': '异常待处理',
  'grading': '阅卷中', 'ready': '待发布', 'published': '已发布'
};

@Entry
@Component
struct ExamListPage {
  @State items: ExamSummary[] = [];
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State q: string = '';
  @State stage: string = '';
  @State navOpen: boolean = false;
  @State publishing: string = '';

  aboutToAppear(): void {
    this.load();
  }

  private async load(): Promise<void> {
    this.loading = this.items.length === 0;
    this.failed = false;
    try {
      this.items = await OgNet.jget<ExamSummary[]>('/api/v1/exams');
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  private filtered(): ExamSummary[] {
    const kw = this.q.trim().toLowerCase();
    const out: ExamSummary[] = [];
    for (const it of this.items) {
      if (this.stage !== '' && it.stage !== this.stage) {
        continue;
      }
      if (kw !== '') {
        const hay = (it.id + ' ' + (it.subject ?? '') + ' ' + (it.examDate ?? '') + ' ' + it.createdBy).toLowerCase();
        if (hay.indexOf(kw) < 0) {
          continue;
        }
      }
      out.push(it);
    }
    return out;
  }

  private async togglePublish(it: ExamSummary): Promise<void> {
    this.publishing = it.id;
    try {
      await OgNet.jpost<Record<string, string>>('/api/v1/exams/' + it.id + '/publish', {
        'published': !it.published
      } as Record<string, boolean>);
      promptAction.showToast({ message: it.published ? '已撤回发布' : '已发布成绩' });
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '操作失败' });
    }
    this.publishing = '';
  }

  private canEditExam(it: ExamSummary): boolean {
    const me = OgSession.me();
    return me !== null && (me.role === 'admin' || me.role === 'leader' || it.mine);
  }

  private primaryAction(it: ExamSummary): void {
    if (it.stage === 'awaiting_scan' || it.stage === 'scanning') {
      router.pushUrl({
        url: 'pages/teacher/UploadScansPage', params: { 'exam': it.id } as Record<string, string>
      }).catch(() => {});
    } else if (it.stage === 'anomalies' || it.stage === 'grading') {
      router.pushUrl({
        url: 'pages/teacher/ExamHubPage', params: { 'exam': it.id } as Record<string, string>
      }).catch(() => {});
    } else if ((it.stage === 'ready' || it.dirty) && this.canEditExam(it)) {
      this.togglePublish(it);
    } else {
      // 已发布:进入考试详情(成绩/统计/导出都在详情页),不再直接生成文档
      router.pushUrl({
        url: 'pages/teacher/ExamHubPage', params: { 'exam': it.id } as Record<string, Object>
      }).catch(() => {});
    }
  }

  @Builder
  navAction() {
    Text('☰').fontSize(20).fontColor(OgC.BRAND).padding({ left: 10, right: 4 })
      .onClick(() => {
        this.navOpen = true;
      })
  }

  @Builder
  sheetBuilder() {
    StaffNavGrid({ current: 'exams', onClose: () => {
      this.navOpen = false;
    } })
  }

  @Builder
  examCard(it: ExamSummary) {
    Column({ space: 8 }) {
      Row({ space: 8 }) {
        Text((it.subject !== undefined && it.subject !== '') ? it.subject : '未标学科')
          .fontSize(15).fontWeight(FontWeight.Bold).fontColor(OgC.INK).layoutWeight(1)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        if (it.appeals !== undefined && it.appeals > 0) {
          Pill({ text: '复核 ' + it.appeals, color: OgC.BAD })
        }
        if (it.flagged !== undefined && it.flagged > 0) {
          Pill({ text: '⚑' + it.flagged, color: OgC.FLAG })
        }
        Pill({ text: it.stageLabel, color: it.published ? OgC.GOOD : (it.stage === 'anomalies' ? OgC.BAD : OgC.WARN) })
      }
      .width('100%')
      Row({ space: 8 }) {
        Text(it.examDate !== undefined && it.examDate !== '' ? it.examDate : it.createdAt)
          .fontSize(11).fontColor(OgC.INK_SUB)
        Text(it.id).fontSize(11).fontColor(OgC.INK_SUB)
        Blank()
        Text(it.students + ' 卷 · 未阅 ' + it.ungraded).fontSize(11).fontColor(OgC.INK_SUB)
      }
      .width('100%')
      if (it.warnings > 0) {
        Text('⚠ ' + it.warnings + ' 份异常卷待裁决')
          .fontSize(12).fontColor(OgC.BAD)
      }
      Row({ space: 8 }) {
        PrimaryButton({
          label: ((it.stage === 'ready' || it.dirty) && !this.canEditExam(it)
            ? '详情' : (it.dirty ? '重新发布' : OgFormat.stageAction(it.stage, it.dirty))),
          usable: this.publishing !== it.id,
          onTap: () => this.primaryAction(it)
        })
        GhostButton({
          label: '详情',
          onTap: () => {
            router.pushUrl({
              url: 'pages/teacher/ExamHubPage', params: { 'exam': it.id } as Record<string, string>
            }).catch(() => {});
          }
        })
      }
      .width('100%')
    }
    .width('100%')
    .padding(14)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
  }

  build() {
    Column() {
      TitleBar({
        title: '考试列表',
        showBack: false,
        actions: (): void => {
          this.navAction()
        }
      })
      // 搜索 + 阶段过滤
      Column({ space: 8 }) {
        Search({ value: this.q, placeholder: '搜索考试 ID / 学科 / 日期' })
          .height(38)
          .onChange((v: string) => {
            this.q = v;
          })
        Scroll() {
          Row({ space: 8 }) {
            ForEach(STAGE_FILTERS, (s: string) => {
              Text(STAGE_NAMES[s])
                .fontSize(12.5)
                .fontColor(this.stage === s ? Color.White : OgC.INK_SUB)
                .fontWeight(this.stage === s ? FontWeight.Medium : FontWeight.Normal)
                .backgroundColor(this.stage === s ? OgC.BRAND : Color.Transparent)
                .padding({ left: 14, right: 14, top: 7, bottom: 7 })
                .borderRadius(16)
                .margin({ right: 8 })
                .onClick(() => {
                  this.stage = s;
                })
            }, (s: string) => s)
          }
        }
        .scrollable(ScrollDirection.Horizontal)
        .width('100%')
      }
      .width('100%')
      .padding({ left: OgDim.PAD, right: OgDim.PAD, top: 8, bottom: 4 })

      Scroll() {
        Column({ space: OgDim.GAP }) {
          if (this.loading) {
            LoadingView()
          } else if (this.failed) {
            ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
          } else if (this.filtered().length === 0) {
            EmptyView({ text: '没有匹配的考试' })
          }
          ForEach(this.filtered(), (it: ExamSummary) => {
            this.examCard(it)
          }, (it: ExamSummary) => it.id + it.stage + String(it.published))
          Text('').height(20)
        }
        .padding({ left: OgDim.PAD, right: OgDim.PAD, bottom: 20 })
        .width('100%')
      }
      .layoutWeight(1)
      .scrollBar(BarState.Off)
      .align(Alignment.Top)
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
    .bindSheet($$this.navOpen, this.sheetBuilder(), {
      height: 360, dragBar: true, title: { title: '工作台导航' }
    })
  }
}
