// 触屏阅卷工作台(Grader Studio 移动版):
//   切片预加载(后 8 张)→ 触屏评分盘(两键合成/±0.5/小数)→ ✓✗⚑ 标注 → 画笔批注
//   → 小问逐问打分自动汇总 → 选做组涂选展示 → 匿名卷隐藏考号 → 409 乐观锁拉取最新
//   → 分片领取租约(claim 兼心跳 90s,TTL 3 分钟;跳过他人持有,翻片/离页释放)
//   → 下一未评/待复核跳转。评分数据契约与 web 端完全一致(POST /api/v1/grades)。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError, ImageCache } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
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
  // 宽屏(md/lg:平板/横屏/折叠屏内屏)左卷右盘双栏;断点实时跟随,折叠展开中自动切换
  @StorageProp('ogBp') bp: string = 'sm';
  // 阅卷分片租约(协作阅卷「谁在阅哪张」,TTL 3 分钟):每端至多持有一张;
  // 只驱动「跳过他人持有」,不拦写路径 —— 正确性由评分乐观锁(409)兜底。
  private heldQ: number = 0;       // 持有租约的题号(0 = 无)
  private heldStudent: number = 0; // 持有租约的学生号
  private heartTimer: number = -1; // 心跳定时器(90s 续期)
  private pageActive: boolean = true;
  private jumping: boolean = false; // 下一未评领取链在途防重入

  aboutToAppear(): void {
    this.startHeartbeat();
    const p = router.getParams() as GraderParams | null;
    if (p === null || p === undefined || p.exam === undefined) {
      this.failed = true;
      this.failMsg = '缺少考试参数';
      this.loading = false;
      return;
    }
    this.examId = p.exam;
    this.init(parseInt(p.q ?? '0', 10), parseInt(p.student ?? '0', 10));
  }

  aboutToDisappear(): void {
    this.stopHeartbeat();
    this.releaseHeld(); // 离开阅卷页:释放租约(失败由 TTL 自愈)
  }

  onPageShow(): void {
    this.pageActive = true;
    this.startHeartbeat();
  }

  onPageHide(): void {
    // 暂离(如跳答卷回溯/切后台):停心跳并释放,回来后写路径会重新领取
    this.pageActive = false;
    this.stopHeartbeat();
    this.releaseHeld();
  }

  private startHeartbeat(): void {
    if (this.heartTimer >= 0) {
      return; // aboutToAppear 与 onPageShow 双入口,已在跑则不重复
    }
    this.heartTimer = setInterval(() => this.renewHeld(), 90000);
  }

  private stopHeartbeat(): void {
    if (this.heartTimer >= 0) {
      clearInterval(this.heartTimer);
      this.heartTimer = -1;
    }
  }

  // 心跳:只为「正在阅的那张」续期(浏览离开的租约交给 TTL 自然过期)
  private renewHeld(): void {
    if (!this.pageActive || this.heldStudent <= 0) {
      return;
    }
    const s = this.curSlice();
    if (this.heldQ !== this.qIndex || s === null || s.student !== this.heldStudent) {
      return;
    }
    this.claimReq(this.heldStudent).catch(() => {});
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
    if (this.meta === null || (this.meta.subjective ?? []).length === 0) {
      this.failed = true;
      this.failMsg = '该考试没有主观题';
      this.loading = false;
      return;
    }
    const q = deepQ >= 1 && deepQ <= (this.meta.subjective ?? []).length ? deepQ : 1;
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
    if (this.meta === null || this.qIndex < 1 || this.qIndex > (this.meta.subjective ?? []).length) {
      return null;
    }
    return (this.meta.subjective ?? [])[this.qIndex - 1];
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

  private loadSeq: number = 0;

  private async loadQuestion(q: number, keepStudent: number): Promise<void> {
    if (this.heldQ > 0 && this.heldQ !== q) {
      await this.releaseHeld(); // 切题释放旧题租约(409 同题重载不受影响)
    }
    const seq = ++this.loadSeq;
    this.loading = true;
    try {
      const arr = await OgNet.jget<SliceItem[]>(
        '/api/v1/exams/' + this.examId + '/slices?q=' + q + '&grading=1');
      if (seq !== this.loadSeq) {
        return; // 已被后续切题请求取代:过期响应不改题号/切片(评审 GraderPage.ets:167)
      }
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
    if (OgSession.lowData()) {
      return; // 省流模式:关闭预加载
    }
    for (let i = 1; i <= 8; i++) {
      const idx = this.cur + i;
      if (idx < this.slices.length && this.slices[idx].missing !== true) {
        ImageCache.prefetch(this.slices[idx].url, OgSession.imageCap(1600));
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

  // 端身份(本端唯一,进程内生成):领取时随请求上报,服务端按 (账号,端) 执行
  // "每端至多持一张" —— 连续翻片丢释放请求时旧片不再占住其他端
  private readonly endId: string =
    'og' + Date.now().toString(36) + Math.floor(Math.random() * 1000000).toString(36);

  private claimReq(student: number): Promise<Record<string, Object>> {
    const body: Record<string, Object> = { 'question': this.qIndex, 'student': student, 'end': this.endId };
    return OgNet.jpost<Record<string, Object>>('/api/v1/exams/' + this.examId + '/slices/claim', body);
  }

  private async releaseHeld(): Promise<void> {
    if (this.heldStudent <= 0) {
      return;
    }
    const q = this.heldQ;
    const st = this.heldStudent;
    this.heldQ = 0;
    this.heldStudent = 0;
    const body: Record<string, Object> = { 'question': q, 'student': st };
    try {
      await OgNet.jpost<Record<string, Object>>('/api/v1/exams/' + this.examId + '/slices/release', body);
    } catch (e) {
      // 释放失败不打扰用户:租约 TTL 过期自愈
    }
  }

  private sliceByStudent(student: number): SliceItem | null {
    for (const it of this.slices) {
      if (it.student === student) {
        return it;
      }
    }
    return null;
  }

  // 载入后已被他人评完:本地置灰(分数以服务端为准,下次整表重拉时回填)
  private markRemotelyGraded(student: number): void {
    const s = this.sliceByStudent(student);
    if (s === null) {
      return;
    }
    if (this.hasParts() && s.parts !== undefined && s.parts !== null) {
      for (const p of s.parts) {
        p.graded = true;
      }
    }
    s.graded = true;
  }

  // ensureHeld 领取当前题下该生的阅卷租约(已持有则免请求;claim 兼心跳)。
  // 返回 'held'(可写)| 'graded'(已被评完,已本地置灰)| 'locked'(他人持有)| 'proceed'
  // (网络故障:放行交给写路径 409 兜底,不因领取失败阻断评分)
  private async ensureHeld(student: number): Promise<string> {
    if (this.heldQ === this.qIndex && this.heldStudent === student) {
      return 'held';
    }
    await this.releaseHeld();
    try {
      const resp = await this.claimReq(student);
      if ((resp['status'] as string) === 'graded') {
        this.markRemotelyGraded(student);
        return 'graded';
      }
      this.heldQ = this.qIndex;
      this.heldStudent = student;
      return 'held';
    } catch (e) {
      if (e instanceof OgError && e.status === 409) {
        const s = this.sliceByStudent(student);
        if (s !== null) {
          s.locked = true;
        }
        return 'locked';
      }
      return 'proceed';
    }
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
        try {
          const ver = await this.postGrade(body);
          p.ver = ver;
        } catch (err) {
          // 部分小问已带新 mark/ink 落库、本地还是旧值:重拉该卷同步服务端状态再抛错,
          // 否则后续 savePart 会携带新版本+旧批注覆盖已存笔迹(评审 GraderPage.ets:386)
          await this.loadQuestion(this.qIndex, s.student);
          throw err as Error;
        }
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
    let diverted = false;
    try {
      const h = await this.ensureHeld(s.student);
      if (h === 'locked') {
        // 他人正阅:不打分,自动跳下一份未评(graded/held/proceed 放行:改分/复核带 ver 正常写)
        diverted = true;
        promptAction.showToast({ message: '⚠ 这份卷正被他人阅,已跳下一份未评', duration: 3200 });
        await this.jumpNextUngraded();
      } else if (this.hasParts() && s.parts !== undefined && s.parts !== null && s.parts.length > 0) {
        await this.savePart(this.curPart, v, true);
        // 自动跳下一问;全部问评完则跳下一份未评
        if (this.autoAdv && this.curPart < s.parts.length - 1) {
          this.curPart++;
        } else if (this.autoAdv) {
          await this.jumpNextUngraded();
        }
      } else {
        await this.saveWhole(v, s.mark ?? '', s.ink ?? [], true);
        if (this.autoAdv) {
          await this.jumpNextUngraded();
        }
      }
      if (!diverted) {
        this.syncTick++;
      }
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
    this.saving = true;
    try {
      const h = await this.ensureHeld(s.student);
      if (h === 'locked') {
        promptAction.showToast({ message: '⚠ 这份卷正被他人阅,标记未保存', duration: 3200 });
      } else {
        const nextMark = s.mark === mark ? '' : mark;
        await this.saveMarkOrInk(nextMark, s.ink ?? []);
        if (nextMark === 'flag') {
          promptAction.showToast({ message: '已标记待复核;复核改分后再清除 ⚑', duration: 3000 });
        }
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

  // 下一未评:落位前先领取租约 —— 他人持有(locked)/已被评完的片由 ensureHeld
  // 本地标记后跳过,两位老师自动错开各自推进;全部不可落时提示已阅完。
  private async jumpNextUngraded(): Promise<void> {
    if (this.jumping) {
      return; // 领取链在途防重入(连点/自动跳重叠)
    }
    this.jumping = true;
    try {
      const s = this.curSlice();
      if (this.hasParts() && s !== null && s.parts !== undefined && s.parts !== null &&
        this.curPart < s.parts.length - 1) {
        return; // 小问未评完不切卷(由 applyScore 推进 curPart)
      }
      // 全圈扫描(从深链进入后半段/手动跳卷时,前面可能仍有未评卷,评审 GraderPage.ets:552)
      const n = this.slices.length;
      let sawUngraded = false, othersLocked = false;
      for (let step = 1; step <= n; step++) {
        const i = (this.cur + step) % n;
        if (!this.ungradedAt(i)) {
          continue;
        }
        sawUngraded = true;
        if (this.slices[i].locked === true) {
          othersLocked = true;
          continue;
        }
        const h = await this.ensureHeld(this.slices[i].student);
        if (h !== 'held' && h !== 'proceed') {
          othersLocked = true;
          continue; // ensureHeld 已本地标记 graded/locked,继续找
        }
        this.cur = i;
        this.curPart = 0;
        this.preload();
        return;
      }
      if (!sawUngraded) {
        promptAction.showToast({ message: '本题已全部阅完' });
      } else if (othersLocked) {
        promptAction.showToast({ message: '剩余未评卷都被他人锁定,稍后会随租约过期释放' });
      } else {
        promptAction.showToast({ message: '本题没有可领取的未评卷' });
      }
    } finally {
      this.jumping = false;
    }
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
    if (this.saving) {
      return; // 保存在途禁跳转(评审 GraderPage.ets:435)
    }
    const n = this.slices.length;
    for (let k = 1; k <= n; k++) {
      const i = (this.cur + k) % n;
      if (this.slices[i].mark === 'flag' && i !== this.cur) {
        if (this.heldStudent > 0) {
          void this.releaseHeld(); // 跳卷离开当前卷即释放租约(评审 GraderPage.ets:603)
        }
        this.cur = i;
        this.curPart = 0;
        this.preload();
        return;
      }
    }
    promptAction.showToast({ message: '本题没有其他待复核卷' });
  }

  private next(): void {
    if (this.saving) {
      return; // 保存在途禁翻片:防止 A 卷分数写进 B 卷目标(评审 GraderPage.ets:435)
    }
    if (this.cur < this.slices.length - 1) {
      if (this.heldStudent > 0) {
        void this.releaseHeld(); // 翻片离开当前卷即释放租约(评审 GraderPage.ets:603)
      }
      this.cur++;
      this.curPart = 0;
      this.preload();
    } else {
      promptAction.showToast({ message: '已是最后一份' });
    }
  }

  private prev(): void {
    if (this.saving) {
      return; // 保存在途禁翻片(评审 GraderPage.ets:435)
    }
    if (this.cur > 0) {
      if (this.heldStudent > 0) {
        void this.releaseHeld(); // 翻片离开当前卷即释放租约(评审 GraderPage.ets:603)
      }
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
      if (this.meta !== null && this.meta.anonymous !== true && this.curReg() !== '') {
        Text('答卷 ›')
          .fontSize(12)
          .fontColor(OgC.BLUE)
          .onClick(() => {
            router.pushUrl({
              url: 'pages/teacher/ReviewPage',
              params: { 'exam': this.examId, 'reg': this.curReg() } as Record<string, string>
            }).catch(() => {});
          })
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
    // 外层铺底色;内容列收口 800、宽屏居中(评审 GraderPage.ets:863)
    Column() {
      Column() {
      // 顶栏
      Row({ space: 8 }) {
        Text('‹').fontSize(26).fontColor(OgC.BRAND).width(34).height(36).textAlign(TextAlign.Center)
          .onClick(() => router.back())
        Column({ space: 1 }) {
          Text('第 ' + (this.meta !== null ? (this.meta.objective ?? []).length + this.qIndex : this.qIndex)
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
        Text('本题 ' + this.qIndex + '/' + (this.meta !== null ? (this.meta.subjective ?? []).length : 0))
          .fontSize(12).fontColor(OgC.INK)
        Blank()
        Text('下一题 ›').fontSize(12).fontColor(OgC.INK_SUB)
          .onClick(() => {
            if (this.meta !== null && this.qIndex < (this.meta.subjective ?? []).length) {
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
      } else if (this.bp !== 'sm') {
        // 平板/横屏双栏:左卷右盘,互不挤占
        Row() {
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
          .layoutWeight(1.55)
          .scrollBar(BarState.Off)
          .align(Alignment.Top)
          Scroll() {
            Column() {
              this.opsArea()
            }
            .width('100%')
          }
          .layoutWeight(1)
          .scrollBar(BarState.Off)
          .align(Alignment.Top)
        }
        .layoutWeight(1)
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
        this.opsArea()
      }
      }
      .width('100%')
      .constraintSize({ maxWidth: OgDim.MAXW })
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
    .alignItems(HorizontalAlign.Center)
  }

  // 操作区(评分盘 + 标记 + 翻卷):窄屏沉底,宽屏右栏。
  @Builder
  opsArea() {
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
          .onClick(() => {
            this.jumpNextUngraded().catch(() => {});
          })
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
