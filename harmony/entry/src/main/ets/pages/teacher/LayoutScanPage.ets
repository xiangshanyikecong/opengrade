// 拍卷建考:手机拍纸质试卷 → 服务端版面识别(/api/v1/layout/analyze,无状态)
// → 逐题人工确认(题型/选项数/分值可改)→ 生成版式 JSON 带入建考 / 批量导入题库。
// 半自动契约:识别草稿由确认页兜底,误检逐题改;适用 A4 单栏印刷体 ±5°。
// 多页累计:每次拍/选一页追加到草稿(对齐 Web 设计器 paperDraft 行为)。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { photoAccessHelper } from '@kit.MediaLibraryKit';
import { fileIo as fs } from '@kit.CoreFileKit';
import { camera, cameraPicker } from '@kit.CameraKit';
import { image } from '@kit.ImageKit';
import { util } from '@kit.ArkTS';
import { OgNet, OgError, OgImageCodec, UploadFile } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgBreakpoint } from '../../common/OgBreakpoint';

import { OgFormat } from '../../common/OgFormat';
import { EmptyView, LoadingView, Pill, PrimaryButton, SectionHeader, TitleBar } from '../../components/Ui';
import { StaffNavGrid, navSheetOptions } from '../../components/StaffNav';

interface AnalyzeQ {
  kind: string;
  options?: number;
  score?: number;
  number?: number;
  note?: string;
  stem?: string;
}

interface AnalyzeS {
  kind: string;
  heightMM?: number;
  note?: string;
  stem?: string;
}

interface AnalyzeResp {
  questions: AnalyzeQ[];
  subjectives: AnalyzeS[];
  notes: string[];
  skewDeg: number;
  width: number;
  height: number;
  overlay: string;
}

// 可编辑草稿行(输入框绑定字符串,应用时再 parse)
interface DraftQ {
  kind: string;
  options: number;
  scoreStr: string;
  number?: number;
  note?: string;
  stem?: string;
  stemPm?: image.PixelMap;
}

interface DraftS {
  heightMM: number;
  scoreStr: string;
  note?: string;
  stem?: string;
  stemPm?: image.PixelMap;
}

const Q_KINDS: string[] = ['choice', 'judge', 'multiple'];

@Entry
@Component
struct LayoutScanPage {
  @State draftQ: DraftQ[] = [];
  @State draftS: DraftS[] = [];
  @State notes: string[] = [];
  @State overlays: image.PixelMap[] = [];
  @State pages: number = 0;
  @State analyzing: boolean = false;
  @State progress: string = '';
  @State failed: string = '';
  @State navOpen: boolean = false;
  // 版式参数
  @State title: string = '';
  @State regDigits: string = '8';
  @State columns: string = '2';
  // 题库导入
  @State bankSubject: string = '';
  @State bankKnowledge: string = '';
  @State bankBusy: boolean = false;
  @State bankMsg: string = '';
  @State tick: number = 0;

  // —— 识别 ——

  private async pickPage(album: boolean): Promise<void> {
    const uris: string[] = [];
    try {
      if (album) {
        const picker = new photoAccessHelper.PhotoViewPicker();
        const opts: photoAccessHelper.PhotoSelectOptions = {
          MIMEType: photoAccessHelper.PhotoViewMIMETypes.IMAGE_TYPE,
          maxSelectNumber: 5
        };
        const result: photoAccessHelper.PhotoSelectResult = await picker.select(opts);
        for (const u of result.photoUris) {
          uris.push(u);
        }
      } else {
        const profile: cameraPicker.PickerProfile = {
          cameraPosition: camera.CameraPosition.CAMERA_POSITION_BACK
        };
        const result: cameraPicker.PickerResult =
          await cameraPicker.pick(getContext(this) as Context, [cameraPicker.PickerMediaType.PHOTO], profile);
        if (result.resultUri !== '') {
          uris.push(result.resultUri);
        }
      }
    } catch (e) {
      return; // 用户取消
    }
    await this.analyzePages(uris);
  }

  private async analyzePages(uris: string[]): Promise<void> {
    if (uris.length === 0 || this.analyzing) {
      return;
    }
    this.analyzing = true;
    this.failed = '';
    let added = 0;
    for (let i = 0; i < uris.length; i++) {
      this.progress = '识别第 ' + (this.pages + 1) + ' 页…';
      try {
        const path = await OgNet.copyUriToCache(uris[i], 'paper_' + Date.now() + '_' + i + '.jpg');
        const files: UploadFile[] = [{
          field: 'image',
          fileName: 'page_' + (this.pages + 1) + '.jpg',
          contentType: 'image/jpeg',
          filePath: path
        } as UploadFile];
        const resp = await OgNet.upload('/api/v1/layout/analyze', {}, files, 120000);
        this.merge(JSON.parse(resp) as AnalyzeResp);
        added++;
      } catch (e) {
        this.failed = '第 ' + (i + 1) + ' 张识别失败:' + (e instanceof OgError ? e.message : '网络异常');
        break;
      }
    }
    this.analyzing = false;
    this.progress = '';
    if (added > 0) {
      promptAction.showToast({ message: '已识别 ' + added + ' 页,请逐题核对' });
    }
  }

  private async merge(resp: AnalyzeResp): Promise<void> {
    for (const q of (resp.questions ?? [])) {
      const kind = (q.kind === 'judge' || q.kind === 'multiple') ? q.kind : 'choice';
      const row: DraftQ = {
        kind: kind,
        options: kind === 'judge' ? 2 : Math.max(2, q.options ?? 4),
        scoreStr: q.score !== undefined && q.score > 0 ? OgFormat.score(q.score) : '1',
        number: q.number,
        note: q.note,
        stem: q.stem,
        stemPm: await this.decodeB64(q.stem, 320)
      };
      this.draftQ.push(row);
    }
    for (const s of (resp.subjectives ?? [])) {
      const row: DraftS = {
        heightMM: Math.max(8, Math.round(s.heightMM ?? 42)),
        scoreStr: '10',
        note: s.note,
        stem: s.stem,
        stemPm: await this.decodeB64(s.stem, 320)
      };
      this.draftS.push(row);
    }
    for (const n of (resp.notes ?? [])) {
      if (this.notes.indexOf(n) < 0) {
        this.notes.push(n);
      }
    }
    if (resp.overlay !== undefined && resp.overlay !== '') {
      const pm = await this.decodeB64(resp.overlay, 1600);
      if (pm !== undefined) {
        this.overlays.push(pm);
      }
    }
    this.pages++;
    this.tick++;
  }

  private async decodeB64(b64: string | undefined, maxDim: number): Promise<image.PixelMap | undefined> {
    if (b64 === undefined || b64 === '') {
      return undefined;
    }
    try {
      const helper = new util.Base64Helper();
      const u8 = helper.decodeSync(b64);
      const ab = u8.buffer.slice(u8.byteOffset, u8.byteOffset + u8.byteLength) as ArrayBuffer;
      return await OgImageCodec.decodeBuf(ab, maxDim);
    } catch (e) {
      return undefined;
    }
  }

  // —— 编辑 ——

  private setQKind(i: number, kind: string): void {
    const q = this.draftQ[i];
    const options = kind === 'judge' ? 2 : (q.kind === 'judge' ? 4 : q.options);
    this.draftQ[i] = {
      kind: kind, options: options, scoreStr: q.scoreStr, number: q.number,
      note: q.note, stem: q.stem, stemPm: q.stemPm
    };
  }

  private setQOptions(i: number, v: string): void {
    const q = this.draftQ[i];
    const n = parseInt(v, 10);
    this.draftQ[i] = {
      kind: q.kind, options: isNaN(n) ? q.options : n, scoreStr: q.scoreStr, number: q.number,
      note: q.note, stem: q.stem, stemPm: q.stemPm
    };
  }

  private setQScore(i: number, v: string): void {
    const q = this.draftQ[i];
    this.draftQ[i] = {
      kind: q.kind, options: q.options, scoreStr: v, number: q.number,
      note: q.note, stem: q.stem, stemPm: q.stemPm
    };
  }

  private setSScore(i: number, v: string): void {
    const s = this.draftS[i];
    this.draftS[i] = { heightMM: s.heightMM, scoreStr: v, note: s.note, stem: s.stem, stemPm: s.stemPm };
  }

  // —— 产出版式 ——

  private buildLayoutJson(): string {
    const questions: Record<string, Object>[] = [];
    for (const q of this.draftQ) {
      const sc = parseFloat(q.scoreStr);
      questions.push({
        'kind': q.kind,
        'options': Math.max(2, q.options),
        'score': isNaN(sc) || sc <= 0 ? 1 : sc
      });
    }
    const subjectives: Record<string, Object>[] = [];
    for (const s of this.draftS) {
      const sc = parseFloat(s.scoreStr);
      subjectives.push({
        'kind': 'subjective',
        'score': isNaN(sc) || sc <= 0 ? 10 : sc,
        'heightMM': Math.max(8, s.heightMM)
      });
    }
    const reg = parseInt(this.regDigits, 10);
    const cols = parseInt(this.columns, 10);
    const layout: Record<string, Object> = {
      'version': 2,
      'title': this.title.trim(),
      'regDigits': isNaN(reg) ? 8 : reg,
      'regMode': 'fill',
      'columns': isNaN(cols) ? 2 : cols,
      'questions': questions,
      'subjectives': subjectives
    };
    return JSON.stringify(layout);
  }

  private applyToCreate(): void {
    if (this.draftQ.length === 0 && this.draftS.length === 0) {
      promptAction.showToast({ message: '还没有识别结果,先拍一页试卷' });
      return;
    }
    const params: Record<string, string> = {
      'presetLayout': this.buildLayoutJson()
    };
    if (this.bankSubject.trim() !== '') {
      params['presetSubject'] = this.bankSubject.trim();
    }
    router.replaceUrl({ url: 'pages/teacher/CreateExamPage', params: params }).catch(() => {});
  }

  // 批量导入题库(客观题/主观题都进;题干截图随题上传;部分失败不回滚,对齐 Web 行为)。
  private async importToBank(): Promise<void> {
    const total = this.draftQ.length + this.draftS.length;
    if (total === 0) {
      promptAction.showToast({ message: '还没有识别结果' });
      return;
    }
    if (this.bankSubject.trim() === '') {
      promptAction.showToast({ message: '请先填写学科' });
      return;
    }
    this.bankBusy = true;
    let ok = 0;
    let fail = 0;
    const ctx = OgSession.context();
    for (const q of this.draftQ) {
      try {
        const sc = parseFloat(q.scoreStr);
        const body: Record<string, Object> = {
          'kind': q.kind,
          'options': Math.max(2, q.options),
          'score': isNaN(sc) || sc <= 0 ? 1 : sc,
          'subject': this.bankSubject.trim(),
          'knowledge': this.bankKnowledge.trim(),
          'stem': ''
        };
        const created = await OgNet.jpost<Record<string, string>>('/api/v1/bank', body);
        await this.uploadStem(created['id'] ?? '', q.stem, q.stemPm, ctx);
        ok++;
      } catch (e) {
        fail++;
      }
    }
    for (const s of this.draftS) {
      try {
        const sc = parseFloat(s.scoreStr);
        const body: Record<string, Object> = {
          'kind': 'subjective',
          'score': isNaN(sc) || sc <= 0 ? 10 : sc,
          'subject': this.bankSubject.trim(),
          'knowledge': this.bankKnowledge.trim(),
          'stem': ''
        };
        const created = await OgNet.jpost<Record<string, string>>('/api/v1/bank', body);
        await this.uploadStem(created['id'] ?? '', s.stem, s.stemPm, ctx);
        ok++;
      } catch (e) {
        fail++;
      }
    }
    this.bankMsg = '已导入 ' + ok + ' 题' + (fail > 0 ? ',失败 ' + fail + ' 题(可到题库手工补录)' : '');
    promptAction.showToast({ message: this.bankMsg });
    this.bankBusy = false;
  }

  private async uploadStem(id: string, stemB64: string | undefined, pm: image.PixelMap | undefined, ctx: Context): Promise<void> {
    if (id === '' || stemB64 === undefined || stemB64 === '') {
      return;
    }
    const name = ctx.cacheDir + '/stem_' + Date.now() + '.jpg';
    try {
      const helper = new util.Base64Helper();
      const u8 = helper.decodeSync(stemB64);
      const ab = u8.buffer.slice(u8.byteOffset, u8.byteOffset + u8.byteLength) as ArrayBuffer;
      const f = fs.openSync(name, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
      try {
        fs.writeSync(f.fd, ab);
      } finally {
        fs.closeSync(f.fd);
      }
      const files: UploadFile[] = [{
        field: 'image',
        fileName: 'stem.jpg',
        contentType: 'image/jpeg',
        filePath: name
      } as UploadFile];
      await OgNet.upload('/api/v1/bank/' + id + '/stem', {}, files);
    } catch (e) {
      // 截图失败不阻断:题已入库
    } finally {
      try {
        fs.unlinkSync(name);
      } catch (e) {
        // 忽略
      }
    }
  }

  private clearDraft(): void {
    this.draftQ = [];
    this.draftS = [];
    this.notes = [];
    this.overlays = [];
    this.pages = 0;
    this.bankMsg = '';
    this.tick++;
  }

  // —— UI ——

  @Builder
  navAction() {
    Text('☰').fontSize(20).fontColor(OgC.BRAND).padding({ left: 10, right: 4 })
      .onClick(() => {
        this.navOpen = true;
      })
  }

  @Builder
  qRow(q: DraftQ, i: number) {
    Column({ space: 5 }) {
      Row({ space: 6 }) {
        Text('第 ' + (q.number !== undefined && q.number > 0 ? q.number : i + 1) + ' 题')
          .fontSize(13).fontWeight(FontWeight.Medium).fontColor(OgC.INK)
        if (q.note !== undefined && q.note !== '') {
          Text('⚠ ' + q.note).fontSize(10).fontColor(OgC.BAD)
            .layoutWeight(1)
            .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        }
        Blank()
        if (q.stemPm !== undefined) {
          Image(q.stemPm).height(26)
            .borderRadius(3)
            .border({ width: 1, color: OgC.LINE })
            .onClick(() => {
              this.showBig(q.stemPm!);
            })
        }
      }
      .width('100%')
      Row({ space: 8 }) {
        ForEach(Q_KINDS, (k: string) => {
          Text(OgFormat.kindName(k))
            .fontSize(11)
            .fontColor(q.kind === k ? Color.White : OgC.INK)
            .backgroundColor(q.kind === k ? OgC.BRAND : OgC.BG)
            .padding({ left: 8, right: 8, top: 4, bottom: 4 })
            .borderRadius(10)
            .onClick(() => this.setQKind(i, k))
        }, (k: string) => k + q.kind)
        Blank()
        Text('选项').fontSize(10).fontColor(OgC.INK_SUB)
        TextInput({ text: String(q.options) })
          .type(InputType.Number).fontSize(12).width(44).height(30)
          .onChange((v: string) => this.setQOptions(i, v))
        Text('分值').fontSize(10).fontColor(OgC.INK_SUB)
        TextInput({ text: q.scoreStr })
          .type(InputType.NUMBER_DECIMAL).fontSize(12).width(48).height(30)
          .onChange((v: string) => this.setQScore(i, v))
      }
      .width('100%')
    }
    .width('100%')
    .padding({ top: 8, bottom: 8 })
    .border({ width: { bottom: 1 }, color: OgC.LINE })
  }

  @Builder
  sRow(s: DraftS, i: number) {
    Row({ space: 8 }) {
      Text('留白区 ' + (i + 1)).fontSize(13).fontWeight(FontWeight.Medium).fontColor(OgC.INK)
      if (s.note !== undefined && s.note !== '') {
        Text('⚠ ' + s.note).fontSize(10).fontColor(OgC.BAD)
          .layoutWeight(1).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
      }
      if (s.stemPm !== undefined) {
        Image(s.stemPm).height(26)
          .borderRadius(3)
          .border({ width: 1, color: OgC.LINE })
          .onClick(() => {
            this.showBig(s.stemPm!);
          })
      }
      Blank()
      Text('分值').fontSize(10).fontColor(OgC.INK_SUB)
      TextInput({ text: s.scoreStr })
        .type(InputType.NUMBER_DECIMAL).fontSize(12).width(48).height(30)
        .onChange((v: string) => this.setSScore(i, v))
    }
    .width('100%')
    .padding({ top: 8, bottom: 8 })
    .border({ width: { bottom: 1 }, color: OgC.LINE })
  }

  private showBig(pm: image.PixelMap): void {
    // 复用整页查看器无需 url:这里用系统级图片预览的成本高于收益,改为轻提示
    promptAction.showToast({ message: '题干小图请到叠加标注图里放大核对' });
  }

  build() {
    Column() {
      TitleBar({
        title: '拍卷建考',
        subtitle: '拍照识别版面 · 人工确认后建考',
        onBack: () => router.back(),
        actions: (): void => {
          this.navAction()
        }
      })
      Scroll() {
        Column({ space: OgDim.GAP }) {
          // 识别入口
          Column({ space: 8 }) {
            SectionHeader({ text: '1. 拍试卷', extra: this.pages > 0 ? '已识别 ' + this.pages + ' 页' : '' })
            Row({ space: 10 }) {
              PrimaryButton({ label: '📷 拍照识页', fill: true, usable: !this.analyzing, onTap: () => this.pickPage(false) })
              PrimaryButton({ label: '🖼 相册多选', fill: true, usable: !this.analyzing, onTap: () => this.pickPage(true) })
            }
            .width('100%')
            if (this.progress !== '') {
              Row({ space: 8 }) {
                LoadingProgress().width(18).height(18).color(OgC.BRAND)
                Text(this.progress).fontSize(12).fontColor(OgC.INK_SUB)
              }
            }
            if (this.failed !== '') {
              Text(this.failed).fontSize(12).fontColor(OgC.BAD)
            }
            Text('整页平拍、四角完整、光线均匀。适用 A4 单栏印刷体试卷(倾斜 ≤5° 自动矫正);双栏/手写会报错。多页可分多次拍,逐页累计。')
              .fontSize(11).fontColor(OgC.INK_SUB)
          }
          .width('100%')
          .padding(14)
          .backgroundColor(OgC.CARD)
          .borderRadius(OgDim.RADIUS)
          .alignItems(HorizontalAlign.Start)

          if (this.pages === 0 && !this.analyzing) {
            EmptyView({ text: '拍一页试卷开始识别' })
          } else {
            // 版式参数
            Column({ space: 8 }) {
              SectionHeader({ text: '2. 版式参数' })
              Row({ space: 10 }) {
                Column({ space: 3 }) {
                  Text('卷面标题').fontSize(11).fontColor(OgC.INK_SUB).width('100%')
                  TextInput({ text: this.title, placeholder: '如 期中数学' })
                    .fontSize(13).height(36).width('100%')
                    .onChange((v: string) => {
                      this.title = v;
                    })
                }
                .alignItems(HorizontalAlign.Start)
                .layoutWeight(1)
                Column({ space: 3 }) {
                  Text('考号位数').fontSize(11).fontColor(OgC.INK_SUB).width('100%')
                  TextInput({ text: this.regDigits })
                    .type(InputType.Number).fontSize(13).height(36).width('100%')
                    .onChange((v: string) => {
                      this.regDigits = v;
                    })
                }
                .alignItems(HorizontalAlign.Start)
                .layoutWeight(1)
                Column({ space: 3 }) {
                  Text('客观题栏数').fontSize(11).fontColor(OgC.INK_SUB).width('100%')
                  TextInput({ text: this.columns })
                    .type(InputType.Number).fontSize(13).height(36).width('100%')
                    .onChange((v: string) => {
                      this.columns = v;
                    })
                }
                .alignItems(HorizontalAlign.Start)
                .layoutWeight(1)
              }
              .width('100%')
              Text('生成的是「涂卡版式」:版面按检测到的题型构成排版,再印答题卡。若要原样复刻试卷版面,请在电脑端设计器微调。')
                .fontSize(11).fontColor(OgC.INK_SUB)
            }
            .width('100%')
            .padding(14)
            .backgroundColor(OgC.CARD)
            .borderRadius(OgDim.RADIUS)
            .alignItems(HorizontalAlign.Start)

            // 检测告警
            if (this.notes.length > 0) {
              Column({ space: 4 }) {
                SectionHeader({ text: '识别提示', extra: '逐条核对' })
                ForEach(this.notes, (n: string) => {
                  Text('⚠ ' + n).fontSize(11).fontColor(OgC.WARN)
                }, (n: string, i: number) => 'n' + i + n)
              }
              .width('100%')
              .padding(12)
              .backgroundColor(OgC.WARN_BG)
              .borderRadius(OgDim.RADIUS)
              .alignItems(HorizontalAlign.Start)
            }

            // 客观题
            Column({ space: 4 }) {
              SectionHeader({ text: '客观题 ' + this.draftQ.length + ' 题', extra: '题型/选项/分值可改' })
              ForEach(this.draftQ, (q: DraftQ, i: number) => {
                this.qRow(q, i)
              }, (q: DraftQ, i: number) => 'q' + i + '-' + q.kind + '-' + q.options + '-' + this.tick)
              if (this.draftQ.length === 0) {
                Text('未检测到客观题').fontSize(12).fontColor(OgC.INK_SUB)
              }
            }
            .width('100%')
            .padding(14)
            .backgroundColor(OgC.CARD)
            .borderRadius(OgDim.RADIUS)
            .alignItems(HorizontalAlign.Start)

            // 主观题
            if (this.draftS.length > 0) {
              Column({ space: 4 }) {
                SectionHeader({ text: '主观留白区 ' + this.draftS.length + ' 处' })
                ForEach(this.draftS, (s: DraftS, i: number) => {
                  this.sRow(s, i)
                }, (s: DraftS, i: number) => 's' + i + '-' + s.heightMM + '-' + this.tick)
              }
              .width('100%')
              .padding(14)
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)
              .alignItems(HorizontalAlign.Start)
            }

            // 叠加标注
            if (this.overlays.length > 0) {
              Column({ space: 6 }) {
                SectionHeader({ text: '检测标注图', extra: '点开核对' })
                ForEach(this.overlays, (pm: image.PixelMap, i: number) => {
                  Image(pm).width('100%')
                    .borderRadius(8)
                    .border({ width: 1, color: OgC.LINE })
                    .objectFit(ImageFit.Contain)
                }, (pm: image.PixelMap, i: number) => 'ov' + i)
              }
              .width('100%')
              .padding(14)
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)
              .alignItems(HorizontalAlign.Start)
            }

            // 题库导入 + 产出版式
            Column({ space: 8 }) {
              SectionHeader({ text: '3. 下一步' })
              Row({ space: 10 }) {
                Column({ space: 3 }) {
                  Text('学科(导入题库必填)').fontSize(11).fontColor(OgC.INK_SUB).width('100%')
                  TextInput({ text: this.bankSubject, placeholder: '如 数学' })
                    .fontSize(13).height(36).width('100%')
                    .onChange((v: string) => {
                      this.bankSubject = v;
                    })
                }
                .alignItems(HorizontalAlign.Start)
                .layoutWeight(1)
                Column({ space: 3 }) {
                  Text('知识点(可选)').fontSize(11).fontColor(OgC.INK_SUB).width('100%')
                  TextInput({ text: this.bankKnowledge, placeholder: '如 函数' })
                    .fontSize(13).height(36).width('100%')
                    .onChange((v: string) => {
                      this.bankKnowledge = v;
                    })
                }
                .alignItems(HorizontalAlign.Start)
                .layoutWeight(1)
              }
              .width('100%')
              if (this.bankMsg !== '') {
                Text(this.bankMsg).fontSize(12).fontColor(OgC.GOOD)
              }
              PrimaryButton({
                full: true,
                usable: !this.bankBusy,
                label: this.bankBusy ? '导入题库中…' : '⇩ 把 ' + (this.draftQ.length + this.draftS.length) + ' 题导入题库',
                onTap: () => this.importToBank()
              })
              PrimaryButton({
                full: true,
                label: '✓ 生成版式并去建考',
                onTap: () => this.applyToCreate()
              })
              Text('清空本次识别结果')
                .fontSize(12)
                .fontColor(OgC.BAD)
                .onClick(() => this.clearDraft())
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
        .constraintSize({ maxWidth: OgDim.MAXW })
      }
      .layoutWeight(1)
      .scrollBar(BarState.Off)
      .align(Alignment.Top)
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
    .bindSheet($$this.navOpen, this.sheetBuilder(), navSheetOptions(OgBreakpoint.now(), '工作台导航'))
  }

  @Builder
  sheetBuilder() {
    StaffNavGrid({ current: 'scanbuild', onClose: () => {
      this.navOpen = false;
    } })
  }
}
