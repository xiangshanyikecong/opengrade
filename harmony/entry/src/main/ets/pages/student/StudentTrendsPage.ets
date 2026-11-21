// 学生/家长端纵向趋势:按学科分组的得分率曲线(含班级均分对照线)与逐场明细。
// 得分率跨卷满分可比;缺考场次断线呈现。视图可被 StudentHomePage 页签复用。

import { router } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { StudentTrendsResp, TrendPoint } from '../../model/Api';
import { EmptyView, ErrorView, LoadingView, Pill, SectionHeader } from '../../components/Ui';
import { LineChart, LineSeries } from '../../components/Charts';

const SERIES_COLORS: string[] = [OgC.BRAND, OgC.BLUE, OgC.WARN, OgC.FLAG, OgC.GOOD, OgC.BAD];

@Component
export struct StudentTrendsView {
  @Prop @Watch('onChild') childOf: string = '';
  @State points: TrendPoint[] = [];
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';

  onChild(): void {
    this.load();
  }

  aboutToAppear(): void {
    this.load();
  }

  private childQ(): string {
    if (OgSession.me() !== null && OgSession.me()!.role === 'parent' && this.childOf !== '') {
      return '?child=' + this.childOf;
    }
    return '';
  }

  private async load(): Promise<void> {
    this.loading = this.points.length === 0;
    try {
      const resp = await OgNet.jget<StudentTrendsResp>('/api/v1/student/trends' + this.childQ());
      this.points = resp.points ?? [];
      this.failed = false;
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  private subjects(): string[] {
    const out: string[] = [];
    for (const p of this.points) {
      const s = p.subject !== undefined && p.subject !== '' ? p.subject : '未标学科';
      if (out.indexOf(s) < 0) {
        out.push(s);
      }
    }
    return out;
  }

  private ptsOf(subject: string): TrendPoint[] {
    const out: TrendPoint[] = [];
    for (const p of this.points) {
      const s = p.subject !== undefined && p.subject !== '' ? p.subject : '未标学科';
      if (s === subject) {
        out.push(p);
      }
    }
    return out;
  }

  private deltaAt(subject: string, idx: number): number {
    const pts = this.ptsOf(subject);
    if (idx <= 0 || idx >= pts.length) {
      return 0;
    }
    if (pts[idx].rank <= 0 || pts[idx - 1].rank <= 0 || pts[idx - 1].absent) {
      return 0;
    }
    return pts[idx - 1].rank - pts[idx].rank;
  }

  @Builder
  subjectBlock(subject: string, color: string) {
    Column({ space: 8 }) {
      SectionHeader({ text: subject })
      LineChart({
        labels: this.ptsOf(subject).map((p: TrendPoint) => p.date),
        series: [
          {
            name: '得分率', color: color,
            values: this.ptsOf(subject).map((p: TrendPoint) => p.rate)
          },
          {
            name: '班级均分', color: OgC.INK_SUB,
            values: this.ptsOf(subject).map((p: TrendPoint) => {
              if (p.classMean !== undefined && p.total > 0) {
                return p.classMean / p.total;
              }
              return NaN;
            })
          }
        ],
        asRate: true,
        chartHeight: 180
      })
      ForEach(this.ptsOf(subject), (p: TrendPoint, idx: number) => {
        Row({ space: 8 }) {
          Text(p.date).fontSize(12).fontColor(OgC.INK_SUB).width(86)
          if (p.absent) {
            Pill({ text: '缺考', color: OgC.WARN })
          } else {
            Text(OgFormat.score(p.score) + '/' + OgFormat.score(p.total))
              .fontSize(13).fontColor(OgC.INK)
            Text('率 ' + OgFormat.pct(p.rate)).fontSize(12).fontColor(OgC.INK_SUB)
            Text('名次 ' + p.rank).fontSize(12).fontColor(OgC.INK_SUB)
            if (this.deltaAt(subject, idx) !== 0) {
              Text(this.deltaAt(subject, idx) >= 0 ? '↑' + this.deltaAt(subject, idx)
                : '↓' + (-this.deltaAt(subject, idx)))
                .fontSize(12)
                .fontColor(this.deltaAt(subject, idx) >= 0 ? OgC.GOOD : OgC.BAD)
            }
          }
          Blank()
        }
        .width('100%')
        .padding({ top: 4, bottom: 4 })
        .border({ width: { bottom: 1 }, color: OgC.LINE })
      }, (p: TrendPoint) => p.examId)
    }
    .width('100%')
    .padding(12)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }

  build() {
    Scroll() {
      Column({ space: OgDim.GAP }) {
        if (this.loading) {
          LoadingView()
        } else if (this.failed) {
          ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
        } else if (this.subjects().length === 0) {
          EmptyView({ text: '还没有可展示的历次成绩' })
        }
        ForEach(this.subjects(), (s: string, idx: number) => {
          this.subjectBlock(s, SERIES_COLORS[idx % SERIES_COLORS.length])
        }, (s: string) => s)
        Text('').height(20)
      }
      .padding(OgDim.PAD)
      .width('100%')
      .constraintSize({ maxWidth: OgDim.MAXW })
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
    .scrollBar(BarState.Off)
    .align(Alignment.Top)
  }
}

// 可直接路由打开的页面包装
@Entry
@Component
struct StudentTrendsPage {
  build() {
    Column() {
      Row() {
        Text('‹')
          .fontSize(26).fontColor(OgC.BRAND).width(36).height(36).textAlign(TextAlign.Center)
          .onClick(() => router.back())
        Text('历次成绩趋势').fontSize(17).fontWeight(FontWeight.Bold).fontColor(OgC.INK)
        Blank()
      }
      .width('100%')
      .padding(12)
      .backgroundColor(OgC.CARD)
      StudentTrendsView()
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
  }
}
