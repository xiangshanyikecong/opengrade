// 触屏阅卷工作台(Grader Studio 移动版):
//   切片预加载(后 8 张)→ 触屏评分盘(两键合成/±0.5/小数)→ ✓✗⚑ 标注 → 画笔批注
//   → 小问逐问打分自动汇总 → 选做组涂选展示 → 匿名卷隐藏考号 → 409 乐观锁拉取最新
//   → 下一未评/待复核跳转。评分数据契约与 web 端完全一致(POST /api/v1/grades)。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError, ImageCache } from '../../common/OgNet';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { ExamDetail, SliceItem, Stroke } from '../../model/Api';
import { ErrorView, LoadingView, Pill } from '../../components/Ui';
import { ScorePad } from '../../components/ScorePad';
import { SliceStage } from '../../components/SliceStage';

interface GraderParams {
  exam: string;
  q: string;
  student: string;
}

@Entry
@Component
struct GraderPage {
  @State examId: string = '';
  @State meta: ExamDetail | null = null;
  @State qIndex: number = 1; // 主观题号(1-based)
  @State slices: SliceItem[] = [];
  @State cur: number = 0;
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State inkOn: boolean = false;
  @State enhance: number = 0;
  @State autoAdv: boolean = true;
  @State curPart: number = 0; // 小问下标(0-based);无小问恒 0
  @State lastScore: number = NaN;
  @State saving: boolean = false;
  @State syncTick: number = 0; // 触发切片区域重渲染

  aboutToAppear(): void {
    const p = router.getParams() as GraderParams | null;
    if (p === null || p.exam === undefined) {
      this.failed = true;
      this.failMsg = '缺少考试参数';
      this.loading = false;
      return;
    }
    this.examId = p.exam;
    this.init(parseInt(p.q ?? '0', 10), parseInt(p.student ?? '0', 10));
  }

  private async init(deepQ: number, deepStudent: number): Promise<void> {
    try {
      this.meta = await OgNet.jget<ExamDetail>('/api/v1/exams/' + this.examId);
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '考试加载失败';
      this.loading = false;
      return;
    }
    if (this.meta === null || this.meta.subjective.length === 0) {
      this.failed = true;
      this.failMsg = '该考试没有主观题';
      this.loading = false;
      return;
    }
    const q = deepQ >= 1 && deepQ <= this.meta.subjective.length ? deepQ : 1;
    await this.loadQuestion(q, 0);
    if (deepStudent > 0) {
      for (let i = 0; i < this.slices.length; i++) {
        if (this.slices[i].student === deepStudent) {
          this.cur = i;
          break;
        }
      }
    }
  }

  private spec(): SubjectiveSpec0 | null {
    if (this.meta === null || this.qIndex < 1 || this.qIndex > this.meta.subjective.length) {
      return null;
    }
    return this.meta.subjective[this.qIndex - 1];
  }

  private hasParts(): boolean {
    const sp = this.spec();
    return sp !== null && sp.parts !== undefined && sp.parts !== null && sp.parts.length > 0;
  }

  private cap(): number {
    const sp = this.spec();
    if (sp === null) {
      return 10;
    }
    if (sp.parts !== undefined && sp.parts !== null && sp.parts.length > 0) {
      if (this.curPart < sp.parts.length) {
        return sp.parts[this.curPart].score;
      }
      return 10;
    }
    return sp.score ?? 10;
  }

  private async loadQuestion(q: number, keepStudent: number): Promise<void> {
    this.loading = true;
    try {
      const arr = await OgNet.jget<SliceItem[]>(
        '/api/v1/exams/' + this.examId + '/slices?q=' + q + '&grading=1');
      this.qIndex = q;
      this.slices = arr ?? [];
      this.curPart = 0;
      if (keepStudent > 0) {
        let found = false;
        for (let i = 0; i < this.slices.length; i++) {
          if (this.slices[i].student === keepStudent) {
            this.cur = i;
            found = true;
            break;
          }
        }
        if (!found) {
          this.cur = 0;
        }
      } else if (this.cur >= this.slices.length) {
        this.cur = 0;
      }
      this.failed = false;
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '题目加载失败';
    }
    this.loading = false;
    this.preload();
  }

  private preload(): void {
    for (let i = 1; i <= 8; i++) {
      const idx = this.cur + i;
      if (idx < this.slices.length && this.slices[idx].missing !== true) {
        ImageCache.prefetch(this.slices[idx].url, 1600);
      }
    }
  }

  private curSlice(): SliceItem | null {
    if (this.cur < 0 || this.cur >= this.slices.length) {
      return null;
    }
    return this.slices[this.cur];
  }

  private curUrl(): string {
    const s = this.curSlice();
    return s !== null ? s.url : '';
  }

  private curInk(): Stroke[] {
    const s = this.curSlice();
    if (s === null || s.ink === undefined || s.ink === null) {
      return [];
    }
    return s.ink;
  }

  private curMissing(): boolean {
    const s = this.curSlice();
    return s !== null && s.missing === true;
  }

  private async postGrade(body: Record<string, Object>): Promise<number> {
    const resp = await OgNet.jpost<Record<string, Object>>('/api/v1/grades', body);
    const ver = resp['ver'] as number;
    return ver;
  }

  // 写整题(无小问):score/mark/ink 一次写入
  private async saveWhole(score: number, mark: string, ink: Stroke[], graded: boolean): Promise<void> {
    const s = this.curSlice();
    if (s === null) {
      return;
    }
    const body: Record<string, Object> = {
      'examId': this.examId,
      'student': s.student,
      'question': this.qIndex,
      'score': score,
      'mark': mark,
      'graded': graded,
      'ink': ink,
      'ver': s.ver ?? 0
    };
    const ver = await this.postGrade(body);
    s.score = score;
    s.mark = mark;
    s.ink = ink;
    s.graded = graded;
    s.ver = ver;
    this.lastScore = score;
  }

  // 写单个小问(拆分小问题):其余小问状态保持
  private async savePart(partIdx: number, score: number, graded: boolean): Promise<void> {
    const s = this.curSlice();
    const sp = this.spec();
    if (s === null || sp === null || s.parts === undefined || s.parts === null) {
      return;
    }
    if (partIdx < 0 || partIdx >= s.parts.length) {
      return;
    }
    const p = s.parts[partIdx];
    const body: Record<string, Object> = {
      'examId': this.examId,
      'student': s.student,
      'question': this.qIndex,
      'part': partIdx + 1,
      'score': score,
      'mark': s.mark ?? '',
      'graded': graded,
      'ink': s.ink ?? [],
      'ver': p.ver ?? 0
    };
    const ver = await this.postGrade(body);
    p.score = score;
    p.graded = graded;
    p.ver = ver;
    this.lastScore = score;
  }

  // mark / ink 变更:与 web 一致同步写进每个小问(读取端取第 1 问)
  private async saveMarkOrInk(mark: string, ink: Stroke[]): Promise<void> {
    const s = this.curSlice();
    if (s === null) {
      return;
    }
    if (this.hasParts() && s.parts !== undefined && s.parts !== null) {
      for (let i = 0; i < s.parts.length; i++) {
        const p = s.parts[i];
        const body: Record<string, Object> = {
          'examId': this.examId,
          'student': s.student,
          'question': this.qIndex,
          'part': i + 1,
          'score': p.score,
          'mark': mark,
          'graded': p.graded,
          'ink': ink,
          'ver': p.ver ?? 0
        };
        const ver = await this.postGrade(body);
        p.ver = ver;
      }
    } else {
      const body: Record<string, Object> = {
        'examId': this.examId,
        'student': s.student,
        'question': this.qIndex,
        'score': s.score,
        'mark': mark,
        'graded': s.graded,
        'ink': ink,
        'ver': s.ver ?? 0
      };
      const ver = await this.postGrade(body);
      s.ver = ver;
    }
    s.mark = mark;
    s.ink = ink;
    this.syncTick++;
  }

  private async applyScore(v: number): Promise<void> {
    const s = this.curSlice();
    if (s === null || this.saving) {
      return;
    }
    if (isNaN(v) || v < 0 || v > this.cap()) {
      promptAction.showToast({ message: '分数须在 0 ~ ' + OgFormat.score(this.cap()) + ' 之间' });
      return;
    }
    this.saving = true;
    try {
      if (this.hasParts() && s.parts !== undefined && s.parts !== null && s.parts.length > 0) {
        await this.savePart(this.curPart, v, true);
        // 自动跳下一问;全部问评完则跳下一份未评
        if (this.autoAdv && this.curPart < s.parts.length - 1) {
          this.curPart++;
        } else if (this.autoAdv) {
          this.jumpNextUngraded();
        }
      } else {
        await this.saveWhole(v, s.mark ?? '', s.ink ?? []);
        if (this.autoAdv) {
          this.jumpNextUngraded();
        }
      }
      this.syncTick++;
    } catch (e) {
      this.onSaveError(e);
    }
    this.saving = false;
  }

  private async applyNudge(delta: number): Promise<void> {
    const s = this.curSlice();
    if (s === null) {
      return;
    }
    let base = 0;
    if (this.hasParts() && s.parts !== undefined && s.parts !== null && this.curPart < s.parts.length) {
      base = s.parts[this.curPart].score;
    } else {
      base = s.graded ? s.score : 0;
    }
    const v = Math.min(this.cap(), Math.max(0, base + delta));
    await this.applyScore(v);
  }

  private async applySame(): Promise<void> {
    if (isNaN(this.lastScore)) {
      promptAction.showToast({ message: '还没有上一份的分数' });
      return;
    }
    await this.applyScore(this.lastScore);
  }

  private async toggleMark(mark: string): Promise<void> {
    const s = this.curSlice();
    if (s === null || this.saving) {
      return;
    }
    const nextMark = s.mark === mark ? '' : mark;
    this.saving = true;
    try {
      await this.saveMarkOrInk(nextMark, s.ink ?? []);
      if (nextMark === 'flag') {
        promptAction.showToast({ message: '已标记待复核;复核改分后再清除 ⚑', duration: 3000 });
      }
    } catch (e) {
      this.onSaveError(e);
    }
    this.saving = false;
  }

  private async addStroke(s: Stroke): Promise<void> {
    const cur = this.curSlice();
    if (cur === null || this.saving) {
      return;
    }
    const ink: Stroke[] = [];
    if (cur.ink !== undefined) {
      for (const st of cur.ink) {
        ink.push(st);
      }
    }
    ink.push(s);
    this.saving = true;
    try {
      await this.saveMarkOrInk(cur.mark ?? '', ink);
    } catch (e) {
      this.onSaveError(e);
    }
    this.saving = false;
  }

  private async clearInk(): Promise<void> {
    const cur = this.curSlice();
    if (cur === null) {
      return;
    }
    this.saving = true;
    try {
      await this.saveMarkOrInk(cur.mark ?? '', []);
    } catch (e) {
      this.onSaveError(e);
    }
    this.saving = false;
  }

  private onSaveError(e: Error): void {
    if (e instanceof OgError && e.status === 409) {
      promptAction.showToast({ message: '⚠ 这份卷刚被他人改过,已拉取最新状态', duration: 3500 });
      this.loadQuestion(this.qIndex, this.curSlice() !== null ? this.curSlice()!.student : 0);
    } else {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '保存失败', duration: 3000 });
    }
  }

  private jumpNextUngraded(): void {
    const s = this.curSlice();
    let start = this.cur + 1;
    if (this.hasParts() && s !== null && s.parts !== undefined && s.parts !== null &&
      this.curPart < s.parts.length - 1) {
      return; // 小问未评完不切卷(由 applyScore 推进 curPart)
    }
    for (let i = start; i < this.slices.length; i++) {
      if (this.ungradedAt(i)) {
        this.cur = i;
        this.curPart = 0;
        this.preload();
        return;
      }
    }
    promptAction.showToast({ message: '本题已全部阅完' });
  }

  private ungradedAt(i: number): boolean {
    const it = this.slices[i];
    if (it.missing === true) {
      return false;
    }
    if (this.hasParts() && it.parts !== undefined && it.parts !== null) {
      for (const p of it.parts) {
        if (!p.graded) {
          return true;
        }
      }
      return false;
    }
    return !it.graded;
  }

  private jumpNextFlagged(): void {
    const n = this.slices.length;
    for (let k = 1; k <= n; k++) {
      const i = (this.cur + k) % n;
      if (this.slices[i].mark === 'flag' && i !== this.cur) {
        this.cur = i;
        this.curPart = 0;
        this.preload();
        return;
      }
    }
    promptAction.showToast({ message: '本题没有其他待复核卷' });
  }

  private next(): void {
    if (this.cur < this.slices.length - 1) {
      this.cur++;
      this.curPart = 0;
      this.preload();
    } else {
      promptAction.showToast({ message: '已是最后一份' });
    }
  }

  private prev(): void {
    if (this.cur > 0) {
      this.cur--;
      this.curPart = 0;
    } else {
      promptAction.showToast({ message: '已是第一份' });
    }
  }

  private gradedCount(): number {
    let n = 0;
    for (const s of this.slices) {
      if (this.hasParts()) {
        if (s.parts !== undefined && s.parts !== null) {
          let all = true;
          for (const p of s.parts) {
            if (!p.graded) {
              all = false;
              break;
            }
          }
          if (all) {
            n++;
          }
        }
      } else if (s.graded) {
        n++;
      }
    }
    return n;
  }

  private curReg(): string {
    const s = this.curSlice();
    return s !== null ? s.reg : '';
  }

  private curRegValid(): boolean {
    const s = this.curSlice();
    return s !== null && s.regValid;
  }

  private curMark(): string {
    const s = this.curSlice();
    return s !== null && s.mark !== undefined ? s.mark : '';
  }

  private curElectiveText(): string {
    const s = this.curSlice();
    if (s === null || s.elective === undefined) {
      return '';
    }
    const e = s.elective;
    let marks = '';
    for (let i = 0; i < e.marks.length; i++) {
      marks += (i > 0 ? ',' : '') + String(e.marks[i] + 1);
    }
    const chosen = e.chosen > 0 ? String(e.chosen) : '无';
    return '选做 ' + e.labels.join('/') + ' · 涂 ' + marks + ' · 计入 ' + chosen;
  }

  @Builder
  infoRow() {
    Row({ space: 8 }) {
      Text('第 ' + (this.cur + 1) + '/' + this.slices.length + ' 卷')
        .fontSize(12).fontColor(OgC.INK_SUB)
      if (this.meta !== null && this.meta.anonymous === true) {
        Pill({ text: '匿名阅卷', color: OgC.BLUE })
      } else if (this.curReg() !== '') {
        Text('考号 ' + this.curReg())
          .fontSize(12)
          .fontColor(this.curRegValid() ? OgC.INK : OgC.BAD)
      }
      if (this.curMark() !== '') {
        Text(OgFormat.markName(this.curMark()))
          .fontSize(14)
          .fontColor(OgFormat.markColor(this.curMark()))
          .fontWeight(FontWeight.Bold)
      }
      Blank()
      if (this.curElectiveText() !== '') {
        Text(this.curElectiveText())
          .fontSize(10)
          .fontColor(OgC.BLUE)
          .maxLines(1)
      }
    }
    .width('100%')
    .padding({ left: OgDim.PAD, right: OgDim.PAD, top: 6, bottom: 6 })
  }

  @Builder
  partChips() {
    Row({ space: 6 }) {
      ForEach(this.spec()?.parts ?? [], (p: PartSpec0, idx: number) => {
        Text('(' + (idx + 1) + ') ' + OgFormat.score(p.score))
          .fontSize(12)
          .fontColor(this.curPart === idx ? Color.White : OgC.INK)
          .backgroundColor(this.curPart === idx ? OgC.BRAND : OgC.CARD)
          .border({ width: 1, color: this.curPart === idx ? OgC.BRAND : OgC.LINE, radius: 10 })
          .padding({ left: 10, right: 10, top: 4, bottom: 4 })
          .onClick(() => {
            this.curPart = idx;
          })
      }, (p: PartSpec0, idx: number) => 'p' + idx + this.syncTick)
      Blank()
      Text('小问打分自动汇总').fontSize(10).fontColor(OgC.INK_SUB)
    }
    .width('100%')
    .padding({ left: OgDim.PAD, right: OgDim.PAD, bottom: 4 })
  }

  build() {
    Column() {
      // 顶栏
      Row({ space: 8 }) {
        Text('‹').fontSize(26).fontColor(OgC.BRAND).width(34).height(36).textAlign(TextAlign.Center)
          .onClick(() => router.back())
        Column({ space: 1 }) {
          Text('第 ' + (this.meta !== null ? this.meta.objective.length + this.qIndex : this.qIndex)
            + ' 题(满分 ' + OgFormat.score(this.cap()) + (this.hasParts() ? ' · 拆小问' : '') + ')')
            .fontSize(15).fontWeight(FontWeight.Bold).fontColor(OgC.INK)
          Text('已阅 ' + this.gradedCount() + '/' + this.slices.length
            + ' · ' + (this.meta !== null ? this.meta.id : ''))
            .fontSize(11).fontColor(OgC.INK_SUB)
            .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        }
        .alignItems(HorizontalAlign.Start)
        .layoutWeight(1)
        Text('自动跳').fontSize(11).fontColor(this.autoAdv ? OgC.BRAND : OgC.INK_SUB)
          .onClick(() => {
            this.autoAdv = !this.autoAdv;
          })
        Text(this.inkOn ? '✏️ 画笔中' : '✏️')
          .fontSize(this.inkOn ? 12 : 18)
          .fontColor(this.inkOn ? Color.White : OgC.INK)
          .backgroundColor(this.inkOn ? OgC.BRAND : OgC.CARD)
          .borderRadius(8)
          .padding(6)
          .onClick(() => {
            this.inkOn = !this.inkOn;
          })
        Text(this.enhance === 0 ? '◐' : (this.enhance === 1 ? '灰' : '反'))
          .fontSize(13).fontColor(OgC.INK)
          .backgroundColor(OgC.CARD)
          .borderRadius(8)
          .padding(6)
          .onClick(() => {
            this.enhance = (this.enhance + 1) % 3;
          })
      }
      .width('100%')
      .padding({ left: 8, right: 10, top: 6, bottom: 6 })
      .backgroundColor(OgC.CARD)

      // 题目切换
      Row({ space: 8 }) {
        Text('‹ 上一题').fontSize(12).fontColor(OgC.INK_SUB)
          .onClick(() => {
            if (this.qIndex > 1) {
              this.loadQuestion(this.qIndex - 1, 0);
            }
          })
        Blank()
        Text('本题 ' + this.qIndex + '/' + (this.meta !== null ? this.meta.subjective.length : 0))
          .fontSize(12).fontColor(OgC.INK)
        Blank()
        Text('下一题 ›').fontSize(12).fontColor(OgC.INK_SUB)
          .onClick(() => {
            if (this.meta !== null && this.qIndex < this.meta.subjective.length) {
              this.loadQuestion(this.qIndex + 1, 0);
            }
          })
      }
      .width('100%')
      .padding({ left: OgDim.PAD, right: OgDim.PAD, top: 4 })

      if (this.loading) {
        LoadingView()
      } else if (this.failed) {
        ErrorView({ msg: this.failMsg, onRetry: () => this.loadQuestion(this.qIndex, 0) })
      } else {
        Scroll() {
          Column() {
            this.infoRow()
            if (this.hasParts()) {
              this.partChips()
            }
            SliceStage({
              url: this.curUrl(),
              strokes: this.curInk(),
              inkOn: this.inkOn,
              enhance: this.enhance,
              missing: this.curMissing(),
              onStrokeDone: (s: Stroke) => this.addStroke(s)
            })
            Text('').height(8)
          }
          .width('100%')
        }
        .layoutWeight(1)
        .scrollBar(BarState.Off)
        .align(Alignment.Top)

        // 操作区
        Column({ space: 6 }) {
          Row({ space: 10 }) {
            Text('✓')
              .fontSize(18).fontColor(OgC.GOOD).padding(6)
              .onClick(() => this.toggleMark('check'))
            Text('✗')
              .fontSize(18).fontColor(OgC.BAD).padding(6)
              .onClick(() => this.toggleMark('cross'))
            Text('⚑')
              .fontSize(18).fontColor(OgC.FLAG).padding(6)
              .onClick(() => this.toggleMark('flag'))
            Blank()
            Text('清笔迹').fontSize(12).fontColor(OgC.INK_SUB).padding(6)
              .onClick(() => this.clearInk())
            Text('待复核 ›').fontSize(12).fontColor(OgC.FLAG).padding(6)
              .onClick(() => this.jumpNextFlagged())
            Text('下一未评 ›').fontSize(12).fontColor(OgC.BRAND).padding(6)
              .onClick(() => this.jumpNextUngraded())
          }
          .width('100%')
          .padding({ left: OgDim.PAD, right: OgDim.PAD })
          ScorePad({
            cap: this.cap(),
            lastScore: this.lastScore,
            onScore: (v: number): void => {
              this.applyScore(v);
            },
            onNudge: (d: number): void => {
              this.applyNudge(d);
            },
            onSame: (): void => {
              this.applySame();
            }
          })
          Row({ space: 10 }) {
            Text('‹ 上一份')
              .fontSize(13).fontColor(OgC.INK)
              .padding({ left: 14, right: 14, top: 10, bottom: 10 })
              .backgroundColor(OgC.CARD)
              .borderRadius(8)
              .onClick(() => this.prev())
            Blank()
            Text('下一份 ›')
              .fontSize(13).fontColor(Color.White)
              .padding({ left: 18, right: 18, top: 10, bottom: 10 })
              .backgroundColor(OgC.BRAND)
              .borderRadius(8)
              .onClick(() => this.next())
          }
          .width('100%')
          .padding({ left: OgDim.PAD, right: OgDim.PAD, bottom: 8 })
        }
        .width('100%')
        .backgroundColor(OgC.BG)
        .border({ width: { top: 1 }, color: OgC.LINE })
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
  }
}

// 局部类型引用(避免整文件依赖过宽)
interface SubjectiveSpec0 {
  kind: string;
  label?: string;
  score?: number;
  parts?: PartSpec0[];
  elective?: string;
}

interface PartSpec0 {
  label?: string;
  score: number;
}
