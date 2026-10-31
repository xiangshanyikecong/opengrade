// 学生/家长端成绩详情:总分卡、逐题"我的作答 vs 参考答案"(错题本)、
// 知识点掌握度、主观题切片、整卷回溯(多页)、成绩复核申请。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
import { OgCache } from '../../common/OgCache';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import {
  StudentAppealItem, StudentExamDetail, StudentQuestion, StudentSliceRef
} from '../../model/Api';
import { ErrorView, KVRow, LoadingView, Pill, SectionHeader } from '../../components/Ui';
import { HBarList, HBarItem, RadarChart, RadarItem } from '../../components/Charts';
import { OgImage } from '../../components/OgImage';

interface DetailParams {
  id: string;
  child: string;
}

@Entry
@Component
struct StudentExamDetailPage {
  @State examId: string = '';
  @State child: string = '';
  @State d: StudentExamDetail | null = null;
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State onlyWrong: boolean = false;
  @State appealText: string = '';
  @State appealBusy: boolean = false;
  @State pageUrls: string[] = [];
  @State offline: boolean = false;

  aboutToAppear(): void {
    const p = router.getParams() as DetailParams | null;
    if (p !== null) {
      this.examId = p.id;
      this.child = p.child ?? '';
    }
    this.load();
  }

  private childQ(): string {
    if (OgSession.me() !== null && OgSession.me()!.role === 'parent' && this.child !== '') {
      return '?child=' + this.child;
    }
    return '';
  }

  private cacheKey(): string {
    return 'student_detail_' + this.examId + (this.child === '' ? '' : '_' + this.child);
  }

  private async load(): Promise<void> {
    this.loading = this.d === null;
    try {
      const resp = await OgNet.jget<StudentExamDetail>('/api/v1/student/exams/' + this.examId + this.childQ());
      this.d = resp;
      this.failed = false;
      this.offline = false;
      OgCache.save(this.cacheKey(), JSON.stringify(resp));
      if (this.d !== null && this.d.pageURL !== undefined && this.pageUrls.length === 0) {
        this.pageUrls = [this.d.pageURL];
      }
    } catch (e) {
      // 断网兜底:最近一次成功拉取的详情
      const cached = OgCache.load(this.cacheKey());
      if (cached !== '') {
        try {
          this.d = JSON.parse(cached) as StudentExamDetail;
          this.offline = true;
          this.failed = false;
          if (this.d !== null && this.d.pageURL !== undefined && this.pageUrls.length === 0) {
            this.pageUrls = [this.d.pageURL];
          }
        } catch (e2) {
          this.failed = true;
          this.failMsg = e instanceof OgError ? e.message : '加载失败';
        }
      } else {
        this.failed = true;
        this.failMsg = e instanceof OgError ? e.message : '加载失败';
      }
    }
    this.loading = false;
  }

  // 成绩单 PDF:应用内预览(可分享/另存)。
  private openReportPdf(): void {
    router.pushUrl({
      url: 'pages/common/PdfViewerPage',
      params: {
        'url': '/api/v1/student/exams/' + this.examId + '/report.pdf' + this.childQ(),
        'title': '成绩单',
        'fileName': '成绩单-' + this.examId + '.pdf'
      } as Record<string, string>
    }).catch(() => {});
  }

  private shownQuestions(): StudentQuestion[] {
    if (this.d === null) {
      return [];
    }
    if (!this.onlyWrong) {
      return this.d.questions;
    }
    const out: StudentQuestion[] = [];
    for (const q of this.d.questions) {
      if (q.wrong || q.pending === true) {
        out.push(q);
      }
    }
    return out;
  }

  private knowledgeItems(): HBarItem[] {
    if (this.d === null) {
      return [];
    }
    const out: HBarItem[] = [];
    for (const k of this.d.knowledge) {
      out.push({ name: k.name, rate: k.rate, extra: k.questions + '题 · ' + OgFormat.pct(k.rate) } as HBarItem);
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

  private async saveReportPdf(): Promise<void> {
    try {
      promptAction.showToast({ message: '正在生成成绩单…' });
      const uri = await OgNet.downloadToPicker(
        '/api/v1/student/exams/' + this.examId + '/report.pdf' + this.childQ(), '成绩单-' + this.examId + '.pdf');
      promptAction.showToast({ message: uri === '' ? '已取消' : '已保存,可在「文件」中查看打印' });
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '下载失败', duration: 3000 });
    }
  }

  private async tryNextPage(): Promise<void> {
    if (this.d === null || this.d.pageURL === undefined) {
      return;
    }
    const next = this.pageUrls.length + 1;
    const url = this.d.pageURL + '?p=' + next;
    try {
      await OgNet.getBytes(url, 60000);
      this.pageUrls.push(url);
    } catch (e) {
      promptAction.showToast({ message: '没有更多页了' });
    }
  }

  private async submitAppeal(): Promise<void> {
    const reason = this.appealText.trim();
    if (reason === '') {
      promptAction.showToast({ message: '请填写复核理由' });
      return;
    }
    this.appealBusy = true;
    try {
      await OgNet.jpost<Record<string, string>>('/api/v1/student/exams/' + this.examId + '/appeals' + this.childQ(), {
        'reason': reason
      } as Record<string, Object>);
      this.appealText = '';
      promptAction.showToast({ message: '复核申请已提交' });
      this.pageUrls = [];
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '提交失败' });
    }
    this.appealBusy = false;
  }

  private openViewer(urls: string[], index: number, title: string): void {
    router.pushUrl({
      url: 'pages/common/ImageViewerPage',
      params: { 'urls': urls, 'index': index, 'title': title } as Record<string, Object>
    }).catch(() => {});
  }

  @Builder
  questionRow(q: StudentQuestion) {
    Row({ space: 8 }) {
      Text(String(q.index))
        .fontSize(12).fontColor(OgC.INK_SUB).width(30).textAlign(TextAlign.Center)
      Column({ space: 2 }) {
        Row({ space: 6 }) {
          Text(OgFormat.kindName(q.kind)).fontSize(11).fontColor(OgC.INK_SUB)
          Text('满分 ' + OgFormat.score(q.max)).fontSize(11).fontColor(OgC.INK_SUB)
          if (q.elective !== undefined && q.elective !== '') {
            Text('选做 ' + q.elective).fontSize(11).fontColor(OgC.BLUE)
          }
        }
        if (q.kind === 'choice' || q.kind === 'judge' || q.kind === 'multiple') {
          Row({ space: 6 }) {
            Text('我的作答 ' + (q.myAnswer !== undefined && q.myAnswer !== '' ? q.myAnswer : '(未答)'))
              .fontSize(13)
              .fontColor(q.right === true ? OgC.GOOD : OgC.BAD)
            Text('参考答案 ' + (q.correct ?? '')).fontSize(13).fontColor(OgC.INK_SUB)
          }
        } else {
          if (q.pending === true) {
            Text('评卷中…').fontSize(13).fontColor(OgC.WARN)
          } else if (q.notCounted === true) {
            Text('未选做,不计分').fontSize(13).fontColor(OgC.INK_SUB)
          } else {
            Text('得分 ' + OgFormat.score(q.myScore) + ' / ' + OgFormat.score(q.max))
              .fontSize(13)
              .fontColor(q.wrong ? OgC.BAD : OgC.GOOD)
          }
        }
      }
      .alignItems(HorizontalAlign.Start)
      .layoutWeight(1)
      if (q.wrong) {
        Pill({ text: '错', color: OgC.BAD })
      } else if (q.pending === true) {
        Pill({ text: '评卷中', color: OgC.WARN })
      } else if (q.right === true) {
        Pill({ text: '对', color: OgC.GOOD })
      }
    }
    .width('100%')
    .padding({ top: 7, bottom: 7 })
    .border({ width: { bottom: 1 }, color: OgC.LINE })
  }

  build() {
    Column() {
      Row() {
        Text('‹').fontSize(26).fontColor(OgC.BRAND).width(36).height(36).textAlign(TextAlign.Center)
          .onClick(() => router.back())
        Column({ space: 1 }) {
          Text(this.d !== null && this.d.subject !== undefined && this.d.subject !== '' ? this.d.subject : '考试详情')
            .fontSize(16).fontWeight(FontWeight.Bold).fontColor(OgC.INK)
          Text(this.d !== null ? this.d.createdAt : '').fontSize(11).fontColor(OgC.INK_SUB)
        }
        .alignItems(HorizontalAlign.Start)
        .layoutWeight(1)
      }
      .width('100%')
      .padding(12)
      .backgroundColor(OgC.CARD)

      if (this.offline) {
        Text('⚠ 当前离线,展示缓存的成绩明细(联网后自动刷新)')
          .fontSize(12).fontColor(OgC.WARN)
          .width('100%')
          .textAlign(TextAlign.Center)
          .padding(8)
          .backgroundColor(OgC.WARN_BG)
      }

      if (this.loading) {
        LoadingView()
      } else if (this.failed) {
        ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
      } else if (this.d !== null) {
        Scroll() {
          Column({ space: OgDim.GAP }) {
            if (this.d.absent === true) {
              Text('本场登记为缺考,不计入统计与排名')
                .fontSize(13).fontColor(OgC.WARN)
                .width('100%')
                .textAlign(TextAlign.Center)
                .padding(12)
                .backgroundColor(OgC.WARN_BG)
                .borderRadius(OgDim.RADIUS)
            } else {
              Column({ space: OgDim.GAP }) {
              Column({ space: 8 }) {
                Row() {
                  Column({ space: 2 }) {
                    Text(OgFormat.score(this.d.score))
                      .fontSize(34).fontWeight(FontWeight.Bold).fontColor(OgC.BRAND)
                    Text('总分 / 满分 ' + OgFormat.score(this.d.total)).fontSize(11).fontColor(OgC.INK_SUB)
                  }
                  Blank()
                  Column({ space: 2 }) {
                    Text('年级 ' + this.d.rank + ' / ' + this.d.count).fontSize(13).fontColor(OgC.INK)
                    Text('班级 ' + this.d.classRank).fontSize(13).fontColor(OgC.INK)
                    Text('均分 ' + OgFormat.score(this.d.mean)).fontSize(12).fontColor(OgC.INK_SUB)
                  }
                  .alignItems(HorizontalAlign.End)
                }
                .width('100%')
                KVRow({ k: '客观题', v: OgFormat.score(this.d.objective) + ' 分' })
                KVRow({ k: '主观题', v: OgFormat.score(this.d.subjective) + ' 分' })
              }
              .width('100%')
              .padding(14)
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)

              Column({ space: 6 }) {
                Row({ space: 8 }) {
                  SectionHeader({ text: '逐题明细(错题本)' })
                  Blank()
                  Text(this.onlyWrong ? '只看错题 ✓' : '只看错题')
                    .fontSize(12)
                    .fontColor(this.onlyWrong ? OgC.BRAND : OgC.INK_SUB)
                    .padding({ left: 10, right: 10, top: 4, bottom: 4 })
                    .borderRadius(12)
                    .backgroundColor(this.onlyWrong ? OgC.BRAND_LIGHT : OgC.BG)
                    .onClick(() => {
                      this.onlyWrong = !this.onlyWrong;
                    })
                }
                .width('100%')
                ForEach(this.shownQuestions(), (q: StudentQuestion) => {
                  this.questionRow(q)
                }, (q: StudentQuestion) => String(q.index) + '-' + String(q.pending) + '-' + String(q.myScore))
                if (this.shownQuestions().length === 0) {
                  Text(this.onlyWrong ? '没有错题,继续保持!' : '暂无题目')
                    .fontSize(12).fontColor(OgC.INK_SUB).padding(10)
                }
              }
              .width('100%')
              .padding(14)
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)
              .alignItems(HorizontalAlign.Start)

              if (this.d.knowledge.length > 0) {
                Column({ space: 8 }) {
                  SectionHeader({ text: '我的知识点掌握度(薄弱排前)' })
                  RadarChart({ items: this.knowledgeRadar(), chartHeight: 220 })
                  HBarList({ items: this.knowledgeItems() })
                }
                .width('100%')
                .padding(14)
                .backgroundColor(OgC.CARD)
                .borderRadius(OgDim.RADIUS)
                .alignItems(HorizontalAlign.Start)
              }

              if (this.d.slices !== undefined && this.d.slices.length > 0) {
                Column({ space: 8 }) {
                  SectionHeader({ text: '我的主观题作答' })
                  ForEach(this.d.slices, (s: StudentSliceRef) => {
                    Column({ space: 4 }) {
                      Text('第 ' + s.question + ' 题').fontSize(12).fontColor(OgC.INK_SUB)
                      OgImage({ url: s.url, maxDim: 1600 })
                        .height(220)
                        .borderRadius(8)
                        .onClick(() => this.openViewer([s.url], 0, '第 ' + s.question + ' 题作答'))
                    }
                    .width('100%')
                  }, (s: StudentSliceRef) => s.url)
                }
                .width('100%')
                .padding(14)
                .backgroundColor(OgC.CARD)
                .borderRadius(OgDim.RADIUS)
                .alignItems(HorizontalAlign.Start)
              }

              if (this.pageUrls.length > 0) {
                Column({ space: 8 }) {
                  SectionHeader({ text: '整卷回溯' })
                  OgImage({ url: this.pageUrls[this.pageUrls.length - 1], maxDim: 2200 })
                    .height(300)
                    .borderRadius(8)
                  Row({ space: 10 }) {
                    Text('第 ' + this.pageUrls.length + ' 页').fontSize(12).fontColor(OgC.INK_SUB)
                    Blank()
                    Text('查看大图 ↗')
                      .fontSize(12).fontColor(OgC.BRAND)
                      .onClick(() => this.openViewer(this.pageUrls, this.pageUrls.length - 1, '整卷回溯'))
                    Text('加载下一页')
                      .fontSize(12).fontColor(OgC.BRAND)
                      .onClick(() => this.tryNextPage())
                  }
                  .width('100%')
                }
                .width('100%')
                .padding(14)
                .backgroundColor(OgC.CARD)
                .borderRadius(OgDim.RADIUS)
                .alignItems(HorizontalAlign.Start)
              }

              Column({ space: 8 }) {
                SectionHeader({ text: '成绩复核' })
                if (this.d.appeals !== undefined && this.d.appeals.length > 0) {
                  ForEach(this.d.appeals, (a: StudentAppealItem) => {
                    Column({ space: 4 }) {
                      Row({ space: 6 }) {
                        Pill({ text: a.status === 'resolved' ? '已回复' : '处理中',
                          color: a.status === 'resolved' ? OgC.GOOD : OgC.WARN })
                        Text(a.time).fontSize(11).fontColor(OgC.INK_SUB)
                      }
                      Text(a.reason).fontSize(12).fontColor(OgC.INK)
                      if (a.reply !== undefined && a.reply !== '') {
                        Text('教师回复:' + a.reply).fontSize(12).fontColor(OgC.BRAND_DEEP)
                      }
                    }
                    .width('100%')
                    .padding(8)
                    .backgroundColor(OgC.BG)
                    .borderRadius(8)
                    .alignItems(HorizontalAlign.Start)
                  }, (a: StudentAppealItem) => a.time)
                }
                TextArea({ text: this.appealText, placeholder: '对成绩有疑问?填写复核理由(哪一题、为什么),提交后教师会回复' })
                  .fontSize(13)
                  .height(76)
                  .maxLength(200)
                  .showCounter(true)
                  .onChange((v: string) => {
                    this.appealText = v;
                  })
                Button(this.appealBusy ? '提交中…' : '提交复核申请')
                  .width('100%').height(40).fontSize(14)
                  .backgroundColor(OgC.BRAND)
                  .enabled(!this.appealBusy)
                  .onClick(() => this.submitAppeal())
              }
              .width('100%')
              .padding(14)
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)
              .alignItems(HorizontalAlign.Start)

              // 非缺考分支内(absent 已窄化为非 true),成绩单按钮恒显示
              Button('预览成绩单 PDF(可分享/打印)')
                .width('100%')
                .height(44)
                .fontSize(14)
                .backgroundColor(OgC.BRAND)
                .onClick(() => this.openReportPdf())
              Button('另存成绩单 PDF')
                .width('100%')
                .height(42)
                .fontSize(14)
                .backgroundColor(OgC.CARD)
                .fontColor(OgC.BRAND)
                .border({ width: 1, color: OgC.BRAND, radius: 10 })
                .onClick(() => this.saveReportPdf())

              if (this.d.wrongQuestions.length > 0) {
                Button('生成错题重练卡(' + this.d.wrongQuestions.length + ' 题)')
                  .width('100%')
                  .height(44)
                  .fontSize(14)
                  .backgroundColor(OgC.BRAND_DEEP)
                  .onClick(() => {
                    router.pushUrl({
                      url: 'pages/student/PracticePage',
                      params: { 'id': this.examId, 'child': this.child } as Record<string, string>
                    }).catch(() => {});
                  })
              }
              Text('').height(24)
              }
              .padding(OgDim.PAD)
              .width('100%')
            }
          }
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
