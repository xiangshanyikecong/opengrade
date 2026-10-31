// 学情分析:统计摘要、分数段分布、逐题难度/区分度、知识点掌握度、班级对比、等级赋分。
// 数据契约:GET /api/v1/exams/{id}/analytics(与 web 端「学情分析」页同口径)。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { AnalyticsResp, ClassStat, QuestionStatView } from '../../model/Api';
import { ErrorView, GhostButton, LoadingView, SectionHeader, TitleBar } from '../../components/Ui';
import { BarChart, HBarList, HBarItem, RadarChart, RadarItem } from '../../components/Charts';

interface AnalyticsParams {
  exam: string;
}

@Entry
@Component
struct AnalyticsPage {
  @State examId: string = '';
  @State d: AnalyticsResp | null = null;
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State converting: boolean = false;

  aboutToAppear(): void {
    const p = router.getParams() as AnalyticsParams | null;
    if (p !== null && p.exam !== undefined) {
      this.examId = p.exam;
    }
    this.load();
  }

  private async load(): Promise<void> {
    this.loading = this.d === null;
    try {
      this.d = await OgNet.jget<AnalyticsResp>('/api/v1/exams/' + this.examId + '/analytics');
      this.failed = false;
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  private async convert(fine: boolean): Promise<void> {
    this.converting = true;
    try {
      await OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/grade-convert', {
        'fine': fine
      } as Record<string, boolean>);
      promptAction.showToast({ message: fine ? '已按 21 级细分档赋分' : '已按五等赋分' });
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '赋分失败' });
    }
    this.converting = false;
  }

  private knowledgeItems(): HBarItem[] {
    if (this.d === null) {
      return [];
    }
    const out: HBarItem[] = [];
    for (const k of this.d.knowledge) {
      out.push({
        name: k.name,
        rate: k.rate,
        extra: k.questions + '题 · ' + OgFormat.pct(k.rate) + ' · ' + k.answers + ' 样本'
      } as HBarItem);
    }
    return out;
  }

  private knowledgeRadar(): RadarItem[] {
    if (this.d === null) {
      return [];
    }
    const out: RadarItem[] = [];
    for (const k of this.d.knowledge) {
      out.push({ name: k.name, rate: k.rate } as RadarItem);
    }
    return out;
  }

  private segLabels(): string[] {
    if (this.d === null) {
      return [];
    }
    const out: string[] = [];
    for (const s of this.d.stats.segments) {
      out.push(OgFormat.score(s.low) + '-' + OgFormat.score(s.high));
    }
    return out;
  }

  private segValues(): number[] {
    if (this.d === null) {
      return [];
    }
    const out: number[] = [];
    for (const s of this.d.stats.segments) {
      out.push(s.count);
    }
    return out;
  }

  @Builder
  statCell(k: string, v: string, color: string) {
    Column({ space: 2 }) {
      Text(v).fontSize(16).fontWeight(FontWeight.Bold).fontColor(color)
      Text(k).fontSize(10).fontColor(OgC.INK_SUB)
    }
    .layoutWeight(1)
    .alignItems(HorizontalAlign.Center)
    .padding({ top: 6, bottom: 6 })
  }

  build() {
    Column() {
      TitleBar({
        title: '学情分析',
        subtitle: (this.d !== null && this.d.subject !== undefined ? this.d.subject + ' · ' : '') + this.examId,
        onBack: () => router.back()
      })
      if (this.loading) {
        LoadingView()
      } else if (this.failed) {
        ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
      } else if (this.d !== null) {
        Scroll() {
          Column({ space: OgDim.GAP }) {
            // 统计摘要
            Column({ space: 6 }) {
              SectionHeader({ text: '统计摘要', extra: '满分 ' + OgFormat.score(this.d.total) })
              Row() {
                this.statCell('实考', String(this.d.stats.count), OgC.INK)
                this.statCell('均分', OgFormat.score(this.d.stats.mean), OgC.BRAND)
                this.statCell('得分率', OgFormat.pct(this.d.stats.meanRate), OgC.BRAND)
                this.statCell('中位数', OgFormat.score(this.d.stats.median), OgC.INK)
              }
              .width('100%')
              Row() {
                this.statCell('最高', OgFormat.score(this.d.stats.max), OgC.INK)
                this.statCell('最低', OgFormat.score(this.d.stats.min), OgC.INK)
                this.statCell('标准差', OgFormat.score(this.d.stats.stdDev), OgC.INK)
                this.statCell('及格率', OgFormat.pct(this.d.stats.passRate), OgC.GOOD)
              }
              .width('100%')
              Row() {
                this.statCell('优秀率', OgFormat.pct(this.d.stats.goodRate), OgC.GOOD)
                this.statCell('低分率', OgFormat.pct(this.d.stats.lowRate), OgC.BAD)
                Blank()
              }
              .width('100%')
            }
            .width('100%')
            .padding(14)
            .backgroundColor(OgC.CARD)
            .borderRadius(OgDim.RADIUS)
            .alignItems(HorizontalAlign.Start)

            // 分数段
            Column({ space: 6 }) {
              SectionHeader({ text: '分数段分布' })
              BarChart({ labels: this.segLabels(), values: this.segValues(), chartHeight: 170 })
            }
            .width('100%')
            .padding(14)
            .backgroundColor(OgC.CARD)
            .borderRadius(OgDim.RADIUS)
            .alignItems(HorizontalAlign.Start)

            // 知识点掌握度
            if (this.d.knowledge.length > 0) {
              Column({ space: 6 }) {
                SectionHeader({ text: '知识点掌握度(薄弱排前)' })
                RadarChart({ items: this.knowledgeRadar(), chartHeight: 230 })
                HBarList({ items: this.knowledgeItems() })
              }
              .width('100%')
              .padding(14)
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)
              .alignItems(HorizontalAlign.Start)
            }

            // 逐题难度
            Column({ space: 6 }) {
              SectionHeader({ text: '逐题难度与区分度', extra: '得分率低排前' })
              ForEach(this.sortedQuestions(), (q: QuestionStatView) => {
                Row({ space: 8 }) {
                  Text(q.label)
                    .fontSize(12).fontColor(OgC.INK).layoutWeight(1)
                    .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
                  Text('率 ' + OgFormat.pct(q.rate))
                    .fontSize(12)
                    .fontColor(q.rate < 0.6 ? OgC.BAD : (q.rate < 0.8 ? OgC.WARN : OgC.GOOD))
                  if (q.discrimination !== undefined && q.discrimination !== 0) {
                    Text('区分 ' + q.discrimination.toFixed(2)).fontSize(11).fontColor(OgC.INK_SUB)
                  }
                  if (q.elective !== undefined && q.elective !== '') {
                    Text('选做' + (q.chosen ?? 0) + '人').fontSize(10).fontColor(OgC.BLUE)
                  }
                }
                .width('100%')
                .padding({ top: 6, bottom: 6 })
                .border({ width: { bottom: 1 }, color: OgC.LINE })
              }, (q: QuestionStatView) => 'q' + q.index)
            }
            .width('100%')
            .padding(14)
            .backgroundColor(OgC.CARD)
            .borderRadius(OgDim.RADIUS)
            .alignItems(HorizontalAlign.Start)

            // 班级对比
            if (this.d.byClass.length > 0) {
              Column({ space: 6 }) {
                SectionHeader({ text: '班级对比' })
                ForEach(this.d.byClass, (c: ClassStat) => {
                  Row({ space: 8 }) {
                    Text(c.class).fontSize(13).fontColor(OgC.INK).layoutWeight(1)
                    Text(c.count + ' 人').fontSize(11).fontColor(OgC.INK_SUB)
                    Text('均分 ' + OgFormat.score(c.mean)).fontSize(12).fontColor(OgC.INK)
                    Text(OgFormat.pct(c.meanRate)).fontSize(12).fontColor(OgC.BRAND)
                  }
                  .width('100%')
                  .padding({ top: 6, bottom: 6 })
                  .border({ width: { bottom: 1 }, color: OgC.LINE })
                }, (c: ClassStat) => c.class)
              }
              .width('100%')
              .padding(14)
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)
              .alignItems(HorizontalAlign.Start)
            }

            // 等级赋分
            Column({ space: 8 }) {
              SectionHeader({ text: '新高考等级赋分' })
              Row({ space: 10 }) {
                GhostButton({ label: this.converting ? '…' : '五等赋分', onTap: () => this.convert(false) })
                GhostButton({ label: '21 级细分档', onTap: () => this.convert(true) })
                Blank()
              }
              .width('100%')
              Text('赋分结果在成绩册与年级总表中查看;可重复执行覆盖。')
                .fontSize(11).fontColor(OgC.INK_SUB)
            }
            .width('100%')
            .padding(14)
            .backgroundColor(OgC.CARD)
            .borderRadius(OgDim.RADIUS)
            .alignItems(HorizontalAlign.Start)
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

  private sortedQuestions(): QuestionStatView[] {
    if (this.d === null) {
      return [];
    }
    const arr: QuestionStatView[] = [];
    for (const q of this.d.questions) {
      arr.push(q);
    }
    arr.sort((a, b) => a.rate - b.rate);
    return arr;
  }
}
