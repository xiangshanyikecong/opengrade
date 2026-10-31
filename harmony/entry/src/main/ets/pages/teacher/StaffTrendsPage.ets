// 教职员纵向趋势:班级均分走势 + 逐生跨考对齐(最近得分/名次/进退步)。
// 班级/学科过滤;数据契约:GET /api/v1/trends?class=&subject=。

import { router } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { StaffTrendsResp, TrendPoint, TrendStudentView } from '../../model/Api';
import { EmptyView, ErrorView, LoadingView, Pill, SectionHeader, TitleBar } from '../../components/Ui';
import { LineChart, LineSeries } from '../../components/Charts';

@Entry
@Component
struct StaffTrendsPage {
  @State resp: StaffTrendsResp | null = null;
  @State selClass: string = '';
  @State selSubject: string = '';
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State expanded: string = '';

  aboutToAppear(): void {
    this.load();
  }

  private async load(): Promise<void> {
    this.loading = this.resp === null;
    this.failed = false;
    try {
      this.resp = await OgNet.jget<StaffTrendsResp>('/api/v1/trends' + OgNet.qs({
        'class': this.selClass,
        'subject': this.selSubject
      }));
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  private latestPoint(st: TrendStudentView): TrendPoint | null {
    for (let i = st.points.length - 1; i >= 0; i--) {
      if (!st.points[i].absent) {
        return st.points[i];
      }
    }
    return null;
  }

  private prevPoint(st: TrendStudentView): TrendPoint | null {
    let seen = 0;
    for (let i = st.points.length - 1; i >= 0; i--) {
      if (!st.points[i].absent) {
        seen++;
        if (seen === 2) {
          return st.points[i];
        }
      }
    }
    return null;
  }

  private latestScore(st: TrendStudentView): string {
    const p = this.latestPoint(st);
    return p !== null ? OgFormat.score(p.score) + '/' + OgFormat.score(p.total) : '-';
  }

  private latestRate(st: TrendStudentView): string {
    const p = this.latestPoint(st);
    return p !== null ? OgFormat.pct(p.rate) : '-';
  }

  private latestRank(st: TrendStudentView): string {
    const p = this.latestPoint(st);
    return p !== null ? String(p.rank) : '-';
  }

  private deltaOf(st: TrendStudentView): number {
    const prev = this.prevPoint(st);
    const cur = this.latestPoint(st);
    if (prev === null || cur === null || prev.rank <= 0 || cur.rank <= 0) {
      return 0;
    }
    return prev.rank - cur.rank;
  }

  private subjects(): string[] {
    if (this.resp === null) {
      return [];
    }
    const out: string[] = [];
    for (const e of this.resp.exams) {
      const sub = e.subject !== undefined && e.subject !== '' ? e.subject : '未标学科';
      if (out.indexOf(sub) < 0) {
        out.push(sub);
      }
    }
    return out;
  }

  private classLabels(): string[] {
    if (this.resp === null) {
      return [];
    }
    const out: string[] = [];
    for (const e of this.resp.exams) {
      // 全科视图下同一天可能有多科考试:标签带学科短名,避免曲线错位误读
      const sub = this.selSubject === '' && e.subject !== undefined && e.subject !== '' ? e.subject + ' ' : '';
      out.push(sub + e.date);
    }
    return out;
  }

  private meanValues(): number[] {
    if (this.resp === null) {
      return [];
    }
    const out: number[] = [];
    for (const e of this.resp.exams) {
      out.push(e.mean);
    }
    return out;
  }

  private classMeanValues(): number[] {
    if (this.resp === null) {
      return [];
    }
    const out: number[] = [];
    for (const e of this.resp.exams) {
      out.push(e.classMean);
    }
    return out;
  }

  @Builder
  studentRow(st: TrendStudentView) {
    Column({ space: 6 }) {
      Row({ space: 8 }) {
        Column({ space: 1 }) {
          Text((st.name !== undefined && st.name !== '' ? st.name + ' ' : '') + st.reg)
            .fontSize(13).fontColor(OgC.INK)
          if (st.class !== undefined && st.class !== '') {
            Text(st.class).fontSize(10).fontColor(OgC.INK_SUB)
          }
        }
        .alignItems(HorizontalAlign.Start)
        .layoutWeight(1)
        if (this.latestPoint(st) !== null) {
          Text(this.latestScore(st)).fontSize(13).fontColor(OgC.INK)
          Text('率 ' + this.latestRate(st)).fontSize(11).fontColor(OgC.INK_SUB)
          Text('名次 ' + this.latestRank(st)).fontSize(11).fontColor(OgC.INK_SUB)
          if (this.deltaOf(st) !== 0) {
            Text(this.deltaOf(st) >= 0 ? '↑' + this.deltaOf(st) : '↓' + (-this.deltaOf(st)))
              .fontSize(12)
              .fontColor(this.deltaOf(st) >= 0 ? OgC.GOOD : OgC.BAD)
          }
        } else {
          Pill({ text: '缺考', color: OgC.WARN })
        }
      }
      .width('100%')
      if (this.expanded === st.reg) {
        ForEach(st.points, (p: TrendPoint) => {
          Row({ space: 8 }) {
            Text(p.date).fontSize(11).fontColor(OgC.INK_SUB).width(84)
            Text(p.subject !== undefined ? p.subject : '-').fontSize(11).fontColor(OgC.INK_SUB).width(50)
            if (p.absent) {
              Pill({ text: '缺考', color: OgC.WARN })
            } else {
              Text(OgFormat.score(p.score) + '/' + OgFormat.score(p.total)).fontSize(11).fontColor(OgC.INK)
              Text('名次 ' + p.rank).fontSize(11).fontColor(OgC.INK_SUB)
            }
            Blank()
          }
          .width('100%')
          .padding({ top: 3, bottom: 3 })
        }, (p: TrendPoint) => p.examId)
      }
    }
    .width('100%')
    .padding({ top: 8, bottom: 8 })
    .border({ width: { bottom: 1 }, color: OgC.LINE })
    .onClick(() => {
      this.expanded = this.expanded === st.reg ? '' : st.reg;
    })
  }

  build() {
    Column() {
      TitleBar({ title: '纵向趋势', showBack: false })
      // 筛选栏:学科 / 班级分组两行展示,左端带固定小标签,避免两组 chips 混在一起难分辨
      Column({ space: 6 }) {
        Scroll() {
          Row({ space: 8 }) {
            Text('学科')
              .fontSize(11).fontColor(OgC.INK_SUB).width(32)
            Text('全科')
              .fontSize(12)
              .fontColor(this.selSubject === '' ? Color.White : OgC.INK)
              .backgroundColor(this.selSubject === '' ? OgC.BLUE : OgC.CARD)
              .padding({ left: 12, right: 12, top: 6, bottom: 6 })
              .borderRadius(14)
              .margin({ right: 8 })
              .onClick(() => {
                this.selSubject = '';
                this.load();
              })
            ForEach(this.subjects(), (sub: string) => {
              Text(sub)
                .fontSize(12)
                .fontColor(this.selSubject === sub ? Color.White : OgC.INK)
                .backgroundColor(this.selSubject === sub ? OgC.BLUE : OgC.CARD)
                .padding({ left: 12, right: 12, top: 6, bottom: 6 })
                .borderRadius(14)
                .margin({ right: 8 })
                .onClick(() => {
                  this.selSubject = this.selSubject === sub ? '' : sub;
                  this.load();
                })
            }, (sub: string) => sub + String(this.selSubject === sub))
          }
        }
        .scrollable(ScrollDirection.Horizontal)
        .width('100%')
        Scroll() {
          Row({ space: 8 }) {
            Text('班级')
              .fontSize(11).fontColor(OgC.INK_SUB).width(32)
            Text('全部班级')
              .fontSize(12)
              .fontColor(this.selClass === '' ? Color.White : OgC.INK)
              .backgroundColor(this.selClass === '' ? OgC.BRAND : OgC.CARD)
              .padding({ left: 12, right: 12, top: 6, bottom: 6 })
              .borderRadius(14)
              .margin({ right: 8 })
              .onClick(() => {
                this.selClass = '';
                this.load();
              })
            ForEach(this.resp !== null ? (this.resp!.classes ?? []) : [], (c: string) => {
              Text(c)
                .fontSize(12)
                .fontColor(this.selClass === c ? Color.White : OgC.INK)
                .backgroundColor(this.selClass === c ? OgC.BRAND : OgC.CARD)
                .padding({ left: 12, right: 12, top: 6, bottom: 6 })
                .borderRadius(14)
                .onClick(() => {
                  this.selClass = this.selClass === c ? '' : c;
                  this.load();
                })
            }, (c: string) => c + String(this.selClass === c))
          }
        }
        .scrollable(ScrollDirection.Horizontal)
        .width('100%')
      }
      .width('100%')
      .padding({ left: OgDim.PAD, right: OgDim.PAD, top: 8, bottom: 4 })

      if (this.loading) {
        LoadingView()
      } else if (this.failed) {
        ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
      } else if (this.resp !== null) {
        Scroll() {
          Column({ space: OgDim.GAP }) {
            if (this.resp.exams.length === 0) {
              EmptyView({ text: '暂无可比考试' })
            } else {
              Column({ space: 6 }) {
                SectionHeader({
                  text: (this.selSubject === '' ? '全体' : this.selSubject)
                    + (this.selClass === '' ? '均分走势' : ' · ' + this.selClass + ' 均分走势')
                })
                LineChart({
                  labels: this.classLabels(),
                  series: [
                    { name: '全体均分', color: OgC.BRAND, values: this.meanValues() },
                    {
                      name: this.selClass === '' ? '班级均分' : this.selClass + '均分',
                      color: OgC.BLUE,
                      values: this.classMeanValues()
                    }
                  ],
                  asRate: false,
                  chartHeight: 190
                })
                Text('横轴为考试日期(升序);名次进退按同口径逐生对齐。')
                  .fontSize(10).fontColor(OgC.INK_SUB)
              }
              .width('100%')
              .padding(14)
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)
              .alignItems(HorizontalAlign.Start)

              Column({ space: 6 }) {
                SectionHeader({ text: '逐生跨考对齐', extra: this.resp.students.length + ' 人 · 点行展开' })
                ForEach(this.resp.students, (st: TrendStudentView) => {
                  this.studentRow(st)
                }, (st: TrendStudentView) => st.reg + '-' + (this.expanded === st.reg ? 'x' : 'y'))
              }
              .width('100%')
              .padding(14)
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)
              .alignItems(HorizontalAlign.Start)
            }
            Text('').height(24)
          }
          .padding(OgDim.PAD)
          .width('100%')
        }
        .layoutWeight(1)
        .scrollBar(BarState.Off)
        .align(Alignment.Top)
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
  }
}
