// 阅卷工作量统计(按评分操作人聚合,算补贴/绩效用):GET /api/v1/workload。
// 口径:一条评分记录(Graded 且 By 非空)= 1 份;拆分小问按问各计 1;历史数据归「(历史)」。

import { router } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgC, OgDim } from '../../common/OgTheme';
import { WorkloadRow } from '../../model/Api';
import { EmptyView, ErrorView, LoadingView, SectionHeader, TitleBar } from '../../components/Ui';
import { NAV_SHEET_HEIGHT, StaffNavGrid } from '../../components/StaffNav';

@Entry
@Component
struct WorkloadPage {
  @State items: WorkloadRow[] = [];
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State navOpen: boolean = false;

  aboutToAppear(): void {
    this.load();
  }

  private async load(): Promise<void> {
    this.loading = this.items.length === 0;
    this.failed = false;
    try {
      this.items = await OgNet.jget<WorkloadRow[]>('/api/v1/workload');
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  private maxGraded(): number {
    let m = 1;
    for (const r of this.items) {
      if (r.graded > m) {
        m = r.graded;
      }
    }
    return m;
  }

  private examCount(r: WorkloadRow): number {
    if (r.byExam === undefined) {
      return 0;
    }
    let n = 0;
    for (const k of Object.keys(r.byExam)) {
      n++;
    }
    return n;
  }

  @Builder
  rowCard(r: WorkloadRow) {
    Column({ space: 6 }) {
      Row({ space: 8 }) {
        Text(r.displayName !== undefined && r.displayName !== '' ? r.displayName : r.username)
          .fontSize(14).fontWeight(FontWeight.Medium).fontColor(OgC.INK).layoutWeight(1)
        if (r.username === '(历史)') {
          Text('历史数据(未记录操作人)').fontSize(10).fontColor(OgC.INK_SUB)
        }
        if (r.flagged > 0) {
          Text('⚑' + r.flagged).fontSize(12).fontColor(OgC.FLAG)
        }
        Text(String(r.graded) + ' 份')
          .fontSize(16).fontWeight(FontWeight.Bold).fontColor(OgC.BRAND)
      }
      .width('100%')
      Progress({ value: r.graded, total: this.maxGraded(), type: ProgressType.Linear })
        .color(OgC.BRAND).height(6).width('100%')
      Row({ space: 8 }) {
        Text('覆盖 ' + this.examCount(r) + ' 场考试').fontSize(11).fontColor(OgC.INK_SUB)
        Blank()
        Text(r.username).fontSize(10).fontColor(OgC.INK_SUB)
      }
      .width('100%')
    }
    .width('100%')
    .padding(12)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }

  build() {
    Column() {
      TitleBar({
        title: '工作量统计',
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
            EmptyView({ text: '暂无评分记录' })
          }
          Column({ space: 6 }) {
            SectionHeader({ text: '按评卷人(已评份数降序)' })
          }
          .width('100%')
          ForEach(this.items, (r: WorkloadRow) => {
            this.rowCard(r)
          }, (r: WorkloadRow) => r.username + '-' + r.graded)
          Text('口径:一条评分记录 = 1 份;拆分小问按问各计 1;仅统计你可见范围的考试。')
            .fontSize(11).fontColor(OgC.INK_SUB)
            .width('100%')
          Text('').height(20)
        }
        .padding(OgDim.PAD)
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
      height: NAV_SHEET_HEIGHT, dragBar: true, title: { title: '工作台导航' }
    })
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
    StaffNavGrid({ current: 'workload', onClose: () => {
      this.navOpen = false;
    } })
  }
}
