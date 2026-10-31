// 学生个体复核页(教师端,对齐 web/review.html 核心闭环):
//   逐题明细(客观题只读对错 + 主观题改分,带 ver 乐观锁与拆小问)+ ✓✗⚑ 标注 +
//   切片与整卷回溯 + 复核申请内联回复裁决 + 拉黑本场。
//   入口:考试详情(⚑队列/复核申请)与阅卷工作台「该生答卷」。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import {
  PartState, StaffStudentDetail, StudentAppealItem, StudentQuestion, SubjectiveState, Stroke
} from '../../model/Api';
import { ErrorView, KVRow, LoadingView, Pill, PrimaryButton, SectionHeader } from '../../components/Ui';
import { OgImage } from '../../components/OgImage';
import { SliceStage } from '../../components/SliceStage';

interface ReviewParams {
  exam: string;
  reg: string;
}

@Entry
@Component
struct ReviewPage {
  @State examId: string = '';
  @State reg: string = '';
  @State d: StaffStudentDetail | null = null;
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State editBuf: Record<string, string> = {}; // s{q} / s{q}p{i} → 分数输入
  @State saving: boolean = false;
  @State sheetKind: string = ''; // '' | reply
  @State sheetOpen: boolean = false;
  @State appealReply: string = '';
  @State pageUrls: string[] = [];

  aboutToAppear(): void {
    const p = router.getParams() as ReviewParams | null;
    if (p !== null && p !== undefined && p.exam !== undefined) {
      this.examId = p.exam;
      this.reg = p.reg;
    }
    this.load();
  }

  private async load(): Promise<void> {
    this.loading = this.d === null;
    this.failed = false;
    try {
      this.d = await OgNet.jget<StaffStudentDetail>(
        '/api/v1/exams/' + this.examId + '/students/' + this.reg);
      if (this.d !== null && this.d.pageURL !== undefined && this.pageUrls.length === 0) {
        this.pageUrls = [this.d.pageURL];
      }
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  private bufKey(q: number, part: number): string {
    return part > 0 ? 's' + q + 'p' + part : 's' + q;
  }

  private bufOf(q: number, part: number): string {
    return this.editBuf[this.bufKey(q, part)] ?? '';
  }

  private setBuf(q: number, part: number, v: string): void {
    this.editBuf[this.bufKey(q, part)] = v;
  }

  private parseBuf(q: number, part: number): number {
    const v = parseFloat(this.bufOf(q, part).trim());
    return isNaN(v) ? -1 : v;
  }

  private subjByQuestion(q: number): SubjectiveState | null {
    if (this.d === null || this.d.subjectives === undefined) {
      return null;
    }
    for (const st of (this.d.subjectives ?? [])) {
      if (st.question === q) {
        return st;
      }
    }
    return null;
  }

  // 改分:拆小问逐问写(各问独立 ver);整题一次写。mark/ink 同步带出(与 web 语义一致)。
  private async saveSubjective(q: number): Promise<void> {
    const st = this.subjByQuestion(q);
    if (st === null || this.saving) {
      return;
    }
    this.saving = true;
    try {
      const parts: PartState[] = st.parts ?? [];
      if (parts.length > 0) {
        for (let i = 0; i < parts.length; i++) {
          const v = this.parseBuf(q, i + 1);
          if (v < 0) {
            continue; // 该问未输入,跳过
          }
          if (v > parts[i].score) {
            promptAction.showToast({ message: '第 ' + (i + 1) + ' 问 ' + v + ' 超过满分 ' + parts[i].score });
            this.saving = false;
            return;
          }
          const body: Record<string, Object> = {
            'examId': this.examId,
            'student': this.d !== null ? this.d.student : 0,
            'question': q,
            'part': i + 1,
            'score': v,
            'mark': st.mark ?? '',
            'graded': true,
            'ink': st.ink !== undefined ? st.ink : [],
            'ver': parts[i].ver ?? 0
          };
          const resp = await OgNet.jpost<Record<string, Object>>('/api/v1/grades', body);
          parts[i].ver = resp['ver'] as number;
          parts[i].score = v;
          parts[i].graded = true;
        }
      } else {
        const v = this.parseBuf(q, 0);
        if (v < 0) {
          promptAction.showToast({ message: '请输入分数' });
          this.saving = false;
          return;
        }
        if (v > st.max) {
          promptAction.showToast({ message: v + ' 超过满分 ' + st.max });
          this.saving = false;
          return;
        }
        const body: Record<string, Object> = {
          'examId': this.examId,
          'student': this.d !== null ? this.d.student : 0,
          'question': q,
          'score': v,
          'mark': st.mark ?? '',
          'graded': true,
          'ink': st.ink !== undefined ? st.ink : [],
          'ver': st.ver ?? 0
        };
        const resp = await OgNet.jpost<Record<string, Object>>('/api/v1/grades', body);
        st.ver = resp['ver'] as number;
        st.score = v;
        st.graded = true;
      }
      this.setBuf(q, 0, '');
      promptAction.showToast({ message: '第 ' + (this.d !== null ? this.d.objN + q : q) + ' 题已保存' });
      await this.load();
    } catch (e) {
      if (e instanceof OgError && e.status === 409) {
        promptAction.showToast({ message: '⚠ 刚被他人改过,已拉取最新', duration: 3200 });
        await this.load();
      } else {
        promptAction.showToast({ message: e instanceof OgError ? e.message : '保存失败', duration: 3000 });
      }
    }
    this.saving = false;
  }

  // 标注:拆小问逐问同步 mark(ink 保持),与 web 端一致
  private async toggleMark(q: number, mark: string): Promise<void> {
    const st = this.subjByQuestion(q);
    if (st === null || this.saving) {
      return;
    }
    this.saving = true;
    const nextMark = st.mark === mark ? '' : mark;
    try {
      const ink: Stroke[] = st.ink !== undefined ? st.ink : [];
      const parts: PartState[] = st.parts ?? [];
      if (parts.length > 0) {
        for (let i = 0; i < parts.length; i++) {
          const body: Record<string, Object> = {
            'examId': this.examId,
            'student': this.d !== null ? this.d.student : 0,
            'question': q,
            'part': i + 1,
            'score': parts[i].score,
            'mark': nextMark,
            'graded': parts[i].graded,
            'ink': ink,
            'ver': parts[i].ver ?? 0
          };
          const resp = await OgNet.jpost<Record<string, Object>>('/api/v1/grades', body);
          parts[i].ver = resp['ver'] as number;
        }
      } else {
        const body: Record<string, Object> = {
          'examId': this.examId,
          'student': this.d !== null ? this.d.student : 0,
          'question': q,
          'score': st.score,
          'mark': nextMark,
          'graded': st.graded,
          'ink': ink,
          'ver': st.ver ?? 0
        };
        const resp = await OgNet.jpost<Record<string, Object>>('/api/v1/grades', body);
        st.ver = resp['ver'] as number;
      }
      st.mark = nextMark;
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '标注失败' });
    }
    this.saving = false;
  }

  private async resolveAppeal(no: number, reply: string): Promise<void> {
    try {
      await OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/appeals/' + no + '/resolve', {
        'reply': reply
      } as Record<string, string>);
      promptAction.showToast({ message: '复核已裁决' });
      this.sheetOpen = false;
      this.sheetKind = '';
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '裁决失败' });
    }
  }

  private async blockAppeal(regNo: string): Promise<void> {
    try {
      await OgNet.jpost<Record<string, Object>>('/api/v1/exams/' + this.examId + '/appeals/block', {
        'reg': regNo, 'blocked': true, 'reason': '复核申请滥用'
      } as Record<string, Object>);
      promptAction.showToast({ message: '已拉黑本场' });
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '操作失败' });
    }
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
        }
        if (q.kind === 'choice' || q.kind === 'judge' || q.kind === 'multiple') {
          Row({ space: 6 }) {
            Text('作答 ' + (q.myAnswer !== undefined && q.myAnswer !== '' ? q.myAnswer : '(未答)'))
              .fontSize(13)
              .fontColor(q.right === true ? OgC.GOOD : OgC.BAD)
            Text('答案 ' + (q.correct ?? '')).fontSize(13).fontColor(OgC.INK_SUB)
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
      } else if (q.right === true) {
        Pill({ text: '对', color: OgC.GOOD })
      }
    }
    .width('100%')
    .padding({ top: 6, bottom: 6 })
    .border({ width: { bottom: 1 }, color: OgC.LINE })
  }

  @Builder
  subjectiveCard(st: SubjectiveState) {
    Column({ space: 8 }) {
      Row({ space: 8 }) {
        Text('第 ' + (this.d !== null ? this.d.objN + st.question : st.question) + ' 题 · 满分 '
          + OgFormat.score(st.max) + (st.elective !== undefined && st.elective !== '' ? ' · 选做' : ''))
          .fontSize(13).fontWeight(FontWeight.Medium).fontColor(OgC.INK).layoutWeight(1)
        if (st.mark !== undefined && st.mark !== '') {
          Text(OgFormat.markName(st.mark))
            .fontSize(15).fontColor(OgFormat.markColor(st.mark)).fontWeight(FontWeight.Bold)
        }
        Text((st.graded ? '已阅 ' + OgFormat.score(st.score) : '未阅'))
          .fontSize(12)
          .fontColor(st.graded ? OgC.INK_SUB : OgC.WARN)
      }
      .width('100%')
      if ((st.parts ?? []).length > 0) {
        ForEach((st.parts ?? []), (pt: PartState, idx: number) => {
          Row({ space: 8 }) {
            Text('(' + (idx + 1) + ') ' + OgFormat.score(pt.score) + ' 分')
              .fontSize(12).fontColor(OgC.INK_SUB).width(64)
            TextInput({ text: this.bufOf(st.question, idx + 1), placeholder: '得分' })
              .type(InputType.NUMBER_DECIMAL)
              .fontSize(13).height(34).layoutWeight(1)
              .onChange((v: string) => this.setBuf(st.question, idx + 1, v))
            Text(pt.graded ? '已阅' : '未阅').fontSize(10).fontColor(pt.graded ? OgC.INK_SUB : OgC.WARN)
          }
          .width('100%')
        }, (pt: PartState, idx: number) => 'p' + idx + '-' + this.bufOf(st.question, idx + 1))
      } else {
        TextInput({ text: this.bufOf(st.question, 0), placeholder: '输入分数(满分 ' + OgFormat.score(st.max) + ')' })
          .type(InputType.NUMBER_DECIMAL)
          .fontSize(14).height(38).width('100%')
          .onChange((v: string) => this.setBuf(st.question, 0, v))
      }
      Row({ space: 12 }) {
        Text('✓').fontSize(17).fontColor(OgC.GOOD).padding(4)
          .onClick(() => this.toggleMark(st.question, 'check'))
        Text('✗').fontSize(17).fontColor(OgC.BAD).padding(4)
          .onClick(() => this.toggleMark(st.question, 'cross'))
        Text('⚑').fontSize(17).fontColor(OgC.FLAG).padding(4)
          .onClick(() => this.toggleMark(st.question, 'flag'))
        Blank()
        PrimaryButton({
          label: this.saving ? '…' : '保存',
          usable: !this.saving,
          onTap: () => this.saveSubjective(st.question)
        })
      }
      .width('100%')
      if (st.sliceURL !== undefined && st.sliceURL !== '') {
        SliceStage({
          url: st.sliceURL,
          strokes: st.ink !== undefined ? st.ink : [],
          inkOn: false,
          enhance: 0,
          missing: false,
          onStrokeDone: (s: Stroke) => {
          }
        })
      }
    }
    .width('100%')
    .padding(12)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }

  build() {
    Column() {
      Row() {
        Text('‹').fontSize(26).fontColor(OgC.BRAND).width(36).height(36).textAlign(TextAlign.Center)
          .onClick(() => router.back())
        Column({ space: 1 }) {
          Text(this.d !== null && this.d.name !== undefined && this.d.name !== '' ? this.d.name : this.reg)
            .fontSize(16).fontWeight(FontWeight.Bold).fontColor(OgC.INK)
          Text('考号 ' + this.reg + (this.d !== null && this.d.classes !== undefined && this.d.classes.length > 0
            ? ' · ' + this.d.classes.join('/') : ''))
            .fontSize(11).fontColor(OgC.INK_SUB)
        }
        .alignItems(HorizontalAlign.Start)
        .layoutWeight(1)
      }
      .width('100%')
      .padding(12)
      .backgroundColor(OgC.CARD)

      if (this.loading) {
        LoadingView()
      } else if (this.failed) {
        ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
      } else if (this.d !== null) {
        Scroll() {
          Column({ space: OgDim.GAP }) {
      if (this.d.absent === true) {
        Text('该生本场登记为缺考')
          .fontSize(13).fontColor(OgC.WARN)
          .width('100%')
          .textAlign(TextAlign.Center)
          .padding(12)
          .backgroundColor(OgC.WARN_BG)
          .borderRadius(OgDim.RADIUS)
      } else {
        Column({ space: OgDim.GAP }) {
        // 成绩摘要
              Column({ space: 6 }) {
                Row() {
                  Column({ space: 2 }) {
                    Text(OgFormat.score(this.d.score ?? 0))
                      .fontSize(30).fontWeight(FontWeight.Bold).fontColor(OgC.BRAND)
                    Text('总分 / 满分 ' + OgFormat.score(this.d.total ?? 0)).fontSize(11).fontColor(OgC.INK_SUB)
                  }
                  Blank()
                  Column({ space: 2 }) {
                    Text('年级 ' + (this.d.rank ?? 0) + '/' + (this.d.count ?? 0)).fontSize(12).fontColor(OgC.INK)
                    Text('均分 ' + OgFormat.score(this.d.mean ?? 0)).fontSize(11).fontColor(OgC.INK_SUB)
                  }
                  .alignItems(HorizontalAlign.End)
                }
                .width('100%')
                Row({ space: 8 }) {
                  Pill({ text: this.d.published ? '已发布' : '未发布', color: this.d.published ? OgC.GOOD : OgC.WARN })
                  if (this.d.dirty) {
                    Pill({ text: '有变更待重发', color: OgC.WARN })
                  }
                  if (this.d.canEdit) {
                    Pill({ text: '可改分', color: OgC.BLUE })
                  }
                  Blank()
                }
                .width('100%')
              }
              .width('100%')
              .padding(14)
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)

              // 逐题明细(客观只读 + 主观可改)
              Column({ space: 6 }) {
                SectionHeader({ text: '逐题明细' })
                ForEach((this.d.questions ?? []), (q: StudentQuestion) => {
                  this.questionRow(q)
                }, (q: StudentQuestion) => 'q' + q.index + String(q.myScore))
              }
              .width('100%')
              .padding(14)
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)
              .alignItems(HorizontalAlign.Start)

              // 主观题改分
              ForEach((this.d.subjectives ?? []), (st: SubjectiveState) => {
                this.subjectiveCard(st)
              }, (st: SubjectiveState) => 's' + st.question + '-' + String(st.score) + '-' + String(st.mark))

              // 整卷回溯
              if (this.pageUrls.length > 0) {
                Column({ space: 8 }) {
                  SectionHeader({ text: '整卷回溯' })
                  OgImage({ url: this.pageUrls[this.pageUrls.length - 1], maxDim: 2200 })
                    .height(280)
                    .borderRadius(8)
                  Row({ space: 10 }) {
                    Text('第 ' + this.pageUrls.length + ' 页').fontSize(12).fontColor(OgC.INK_SUB)
                    Blank()
                    Text('查看大图 ↗')
                      .fontSize(12).fontColor(OgC.BRAND)
                      .onClick(() => this.openViewer(this.pageUrls, this.pageUrls.length - 1, '整卷回溯'))
                    if (this.d !== null && this.pageUrls.length < (this.d.pages ?? 1)) {
                      Text('加载下一页')
                        .fontSize(12).fontColor(OgC.BRAND)
                        .onClick(() => {
                          if (this.d !== null && this.d.pageURL !== undefined) {
                            this.pageUrls.push(this.d.pageURL + '?p=' + (this.pageUrls.length + 1));
                          }
                        })
                    }
                  }
                  .width('100%')
                }
                .width('100%')
                .padding(14)
                .backgroundColor(OgC.CARD)
                .borderRadius(OgDim.RADIUS)
                .alignItems(HorizontalAlign.Start)
              }

              // 复核申请
              Column({ space: 8 }) {
                SectionHeader({ text: '复核申请' })
                if ((this.d.appeals ?? []).length === 0) {
                  Text('该生本场暂无复核申请').fontSize(12).fontColor(OgC.INK_SUB)
                }
                ForEach((this.d.appeals ?? []), (a: StudentAppealItem, idx: number) => {
                  Column({ space: 4 }) {
                    Row({ space: 6 }) {
                      Pill({
                        text: a.status === 'resolved' ? '已回复' : '待处理',
                        color: a.status === 'resolved' ? OgC.GOOD : OgC.BAD
                      })
                      Text(a.time).fontSize(11).fontColor(OgC.INK_SUB)
                    }
                    Text(a.reason).fontSize(12).fontColor(OgC.INK)
                    if (a.reply !== undefined && a.reply !== '') {
                      Text('回复:' + a.reply).fontSize(11).fontColor(OgC.BRAND_DEEP)
                    }
                    if (a.status !== 'resolved' && this.d !== null && this.d.canEdit) {
                      Row({ space: 10 }) {
                        Text('回复裁决 ›').fontSize(12).fontColor(OgC.BRAND)
                          .onClick(() => {
                            this.appealReply = '';
                            this.sheetKind = 'reply';
                            this.sheetOpen = true;
                          })
                        Text('拉黑本场').fontSize(12).fontColor(OgC.BAD)
                          .onClick(() => this.blockAppeal(this.reg))
                        Blank()
                      }
                      .width('100%')
                    }
                  }
                  .width('100%')
                  .padding(8)
                  .backgroundColor(OgC.BG)
                  .borderRadius(8)
                  .alignItems(HorizontalAlign.Start)
                }, (a: StudentAppealItem, idx: number) => 'a' + idx + '-' + a.status)
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
          }
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
    .bindSheet($$this.sheetOpen, this.anySheetBuilder(), {
      height: SheetSize.FIT_CONTENT, dragBar: true
    })
  }

  @Builder
  anySheetBuilder() {
    if (this.sheetKind === 'reply') {
      Column({ space: 10 }) {
        TextArea({ text: this.appealReply, placeholder: '填写复核结论回复(学生端可见)' })
          .fontSize(13)
          .height(100)
          .onChange((v: string) => {
            this.appealReply = v;
          })
        PrimaryButton({
          full: true,
          label: '回复并裁决',
          onTap: () => {
            const reply = this.appealReply.trim();
            if (reply === '') {
              promptAction.showToast({ message: '请填写回复内容' });
              return;
            }
            let no = 0;
            if (this.d !== null) {
              const aps: StudentAppealItem[] = this.d.appeals ?? [];
              for (let i = 0; i < aps.length; i++) {
                if (aps[i].status !== 'resolved') {
                  no = i + 1;
                  break;
                }
              }
            }
            if (no === 0) {
              promptAction.showToast({ message: '没有待处理的申请' });
              return;
            }
            this.sheetOpen = false;
            this.sheetKind = '';
            this.resolveAppeal(no, reply);
          }
        })
      }
      .padding(OgDim.PAD)
      .width('100%')
    }
  }
}

