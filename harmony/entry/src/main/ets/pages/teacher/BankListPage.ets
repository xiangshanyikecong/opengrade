// 校本题库(移动端管理):列表/搜索/新建/编辑/删除 + 题干截图拍照上传。
// 权限沿用服务端口径:创建者、管理员、年级组长可改删。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { photoAccessHelper } from '@kit.MediaLibraryKit';
import { fileIo as fs } from '@kit.CoreFileKit';
import { camera, cameraPicker } from '@kit.CameraKit';
import { OgNet, OgError, UploadFile } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { BankQuestion } from '../../model/Api';
import { EmptyView, ErrorView, LoadingView, Pill, PrimaryButton, SectionHeader, TitleBar } from '../../components/Ui';
import { OgImage } from '../../components/OgImage';
import { NAV_SHEET_HEIGHT, StaffNavGrid } from '../../components/StaffNav';

const KINDS: string[] = ['choice', 'judge', 'multiple', 'fillblank', 'subjective'];
const DIFFS: string[] = ['容易', '中等', '较难', '难'];

@Entry
@Component
struct BankListPage {
  @State items: BankQuestion[] = [];
  @State q: string = '';
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State navOpen: boolean = false;
  // 编辑表单(新建/编辑共用)
  @State formOpen: boolean = false;
  @State editId: string = ''; // '' = 新建
  @State fSubject: string = '';
  @State fKind: string = 'choice';
  @State fOptions: string = '4';
  @State fScore: string = '2';
  @State fStem: string = '';
  @State fAnswer: string = '';
  @State fKnowledge: string = '';
  @State fDifficulty: string = '';
  @State busy: boolean = false;
  @State stemPickedName: string = '';

  aboutToAppear(): void {
    this.load();
  }

  private canManage(it: BankQuestion): boolean {
    const me = OgSession.me();
    return me !== null && (OgSession.isManager() || me.username === it.createdBy);
  }

  private async load(): Promise<void> {
    this.loading = this.items.length === 0;
    this.failed = false;
    try {
      this.items = await OgNet.jget<BankQuestion[]>('/api/v1/bank' + OgNet.qs({
        'subject': '', 'kind': '', 'q': this.q.trim()
      }));
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  private openForm(it: BankQuestion | null): void {
    if (it === null) {
      this.editId = '';
      this.fSubject = '';
      this.fKind = 'choice';
      this.fOptions = '4';
      this.fScore = '2';
      this.fStem = '';
      this.fAnswer = '';
      this.fKnowledge = '';
      this.fDifficulty = '';
    } else {
      this.editId = it.id;
      this.fSubject = it.subject ?? '';
      this.fKind = it.kind;
      this.fOptions = it.options !== undefined ? String(it.options) : '';
      this.fScore = it.score !== undefined ? OgFormat.score(it.score) : '';
      this.fStem = it.stem ?? '';
      this.fAnswer = it.answer ?? '';
      this.fKnowledge = it.knowledge ?? '';
      this.fDifficulty = it.difficulty ?? '';
    }
    this.stemPickedName = '';
    this.formOpen = true;
  }

  private async saveForm(): Promise<void> {
    if (this.fSubject.trim() === '' || this.fStem.trim() === '') {
      promptAction.showToast({ message: '学科与题干为必填' });
      return;
    }
    const body: Record<string, Object> = {
      'subject': this.fSubject.trim(),
      'kind': this.fKind,
      'stem': this.fStem.trim(),
      'answer': this.fAnswer.trim(),
      'knowledge': this.fKnowledge.trim(),
      'difficulty': this.fDifficulty.trim()
    };
    if (this.fKind === 'choice' || this.fKind === 'multiple') {
      const opts = parseInt(this.fOptions.trim(), 10);
      if (!(opts >= 2 && opts <= 6)) {
        promptAction.showToast({ message: '选项数须为 2~6' });
        return;
      }
      body['options'] = opts;
    }
    const sc = parseFloat(this.fScore.trim());
    if (!isNaN(sc) && sc > 0) {
      body['score'] = sc;
    }
    this.busy = true;
    try {
      if (this.editId === '') {
        const created = await OgNet.jpost<BankQuestion>('/api/v1/bank', body);
        promptAction.showToast({ message: '已录入:' + created.id });
        if (this.stemPickedName !== '') {
          await this.uploadStem(created.id);
        }
      } else {
        await OgNet.jpost<Record<string, Object>>('/api/v1/bank/' + this.editId, body);
        if (this.stemPickedName !== '') {
          await this.uploadStem(this.editId);
        }
        promptAction.showToast({ message: '已保存' });
      }
      this.formOpen = false;
      this.stemPickedName = '';
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '保存失败', duration: 3200 });
    }
    this.busy = false;
  }

  // 题干截图:相册多选/拍照 → 拷入沙箱 → multipart 上传(服务端字段 image)
  private async pickStem(album: boolean): Promise<void> {
    const uris: string[] = [];
    try {
      if (album) {
        const picker = new photoAccessHelper.PhotoViewPicker();
        const opts: photoAccessHelper.PhotoSelectOptions = {
          MIMEType: photoAccessHelper.PhotoViewMIMETypes.IMAGE_TYPE,
          maxSelectNumber: 1
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
    if (uris.length === 0) {
      return;
    }
    try {
      await OgNet.copyUriToCache(uris[0], 'stem_' + Date.now() + '.jpg');
      this.stemPickedName = '已选截图(保存时上传)';
      promptAction.showToast({ message: '截图已就绪,保存题目时上传' });
    } catch (e) {
      promptAction.showToast({ message: '图片读取失败' });
    }
  }

  private async uploadStem(id: string): Promise<void> {
    const files: UploadFile[] = [];
    const ctx = OgSession.context();
    const names: string[] = fsListNames(ctx.cacheDir);
    for (const n of names) {
      if (n.startsWith('stem_')) {
        files.push({
          field: 'image',
          fileName: n,
          contentType: 'image/jpeg',
          filePath: ctx.cacheDir + '/' + n
        } as UploadFile);
      }
    }
    if (files.length === 0) {
      return;
    }
    await OgNet.upload('/api/v1/bank/' + id + '/stem', {}, files);
    // 只清理本次 stem 临时拷贝(不动全局图片缓存)
    for (const f of files) {
      try {
        fs.unlinkSync(f.filePath);
      } catch (e) {
        // 忽略
      }
    }
  }

  private confirmDelete(it: BankQuestion): void {
    AlertDialog.show({
      message: '确认删除题目 ' + it.id + '?删除后不可恢复(相关考试不受影响)。',
      title: '删除题目',
      autoCancel: true,
      alignment: DialogAlignment.Center,
      primaryButton: {
        value: '取消',
        action: () => {
        }
      },
      secondaryButton: {
        value: '确认删除',
        fontColor: OgC.BAD,
        action: () => this.deleteQuestion(it)
      }
    });
  }

  private async deleteQuestion(it: BankQuestion): Promise<void> {
    try {
      await OgNet.jdel<Record<string, string>>('/api/v1/bank/' + it.id);
      promptAction.showToast({ message: '已删除' });
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '删除失败' });
    }
  }

  @Builder
  navAction() {
    Row({ space: 10 }) {
      Text('＋').fontSize(20).fontColor(OgC.BRAND)
        .onClick(() => this.openForm(null))
      Text('☰').fontSize(20).fontColor(OgC.BRAND).padding({ right: 4 })
        .onClick(() => {
          this.navOpen = true;
        })
    }
  }

  @Builder
  bankCard(it: BankQuestion) {
    Column({ space: 6 }) {
      Row({ space: 8 }) {
        Pill({ text: OgFormat.kindName(it.kind), color: OgC.BLUE })
        if (it.subject !== undefined && it.subject !== '') {
          Text(it.subject).fontSize(12).fontColor(OgC.INK_SUB)
        }
        if (it.difficulty !== undefined && it.difficulty !== '') {
          Pill({ text: it.difficulty, color: OgC.WARN })
        }
        Blank()
        if (it.score !== undefined && it.score > 0) {
          Text(OgFormat.score(it.score) + ' 分').fontSize(12).fontColor(OgC.BRAND)
        }
      }
      .width('100%')
      if (it.stem !== undefined && it.stem !== '') {
        Text(it.stem).fontSize(13).fontColor(OgC.INK)
          .maxLines(3).textOverflow({ overflow: TextOverflow.Ellipsis })
      }
      if (it.stemImage !== undefined && it.stemImage !== '') {
        OgImage({ url: '/api/v1/bank/' + it.id + '/stem', maxDim: 900 })
          .height(120)
          .borderRadius(8)
      }
      if (it.answer !== undefined && it.answer !== '') {
        Text('参考答案:' + it.answer).fontSize(12).fontColor(OgC.GOOD)
      }
      if (it.knowledge !== undefined && it.knowledge !== '') {
        Text('知识点:' + it.knowledge).fontSize(11).fontColor(OgC.INK_SUB)
      }
      Row({ space: 12 }) {
        if (it.usedIn !== undefined && it.usedIn.length > 0) {
          Text('被 ' + it.usedIn.length + ' 场考试使用').fontSize(10).fontColor(OgC.INK_SUB)
        }
        Blank()
        if (this.canManage(it)) {
          Text('编辑').fontSize(12).fontColor(OgC.BRAND)
            .onClick(() => this.openForm(it))
          Text('删除').fontSize(12).fontColor(OgC.BAD)
            .onClick(() => this.confirmDelete(it))
        } else {
          Text('仅创建者/组长/管理员可改').fontSize(10).fontColor(OgC.INK_SUB)
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

  build() {
    Column() {
      TitleBar({
        title: '校本题库',
        showBack: false,
        actions: (): void => {
          this.navAction()
        }
      })
      Row({ space: 8 }) {
        Search({ value: this.q, placeholder: '搜索题干/知识点' })
          .height(38).layoutWeight(1)
          .onChange((v: string) => {
            this.q = v;
          })
          .onSubmit(() => this.load())
        Text('查询').fontSize(13).fontColor(OgC.BRAND).onClick(() => this.load())
      }
      .width('100%')
      .padding({ left: OgDim.PAD, right: OgDim.PAD, top: 8, bottom: 4 })

      Scroll() {
        Column({ space: OgDim.GAP }) {
          if (this.loading) {
            LoadingView()
          } else if (this.failed) {
            ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
          } else if (this.items.length === 0) {
            EmptyView({ text: '题库为空,点右上角 ＋ 录入第一题' })
          }
          ForEach(this.items, (it: BankQuestion) => {
            this.bankCard(it)
          }, (it: BankQuestion) => it.id + String(it.answer))
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
    .bindSheet($$this.formOpen, this.formBuilder(), {
      height: SheetSize.FIT_CONTENT, dragBar: true, title: { title: this.editId === '' ? '录入新题' : '编辑题目' }
    })
  }

  @Builder
  sheetBuilder() {
    StaffNavGrid({ current: 'bank', onClose: () => {
      this.navOpen = false;
    } })
  }

  @Builder
  formBuilder() {
    Scroll() {
      Column({ space: 8 }) {
        Row({ space: 8 }) {
          Column({ space: 3 }) {
            Text('学科 *').fontSize(11).fontColor(OgC.INK_SUB).width('100%')
            TextInput({ text: this.fSubject, placeholder: '如 数学' })
              .fontSize(14).height(38).width('100%')
              .onChange((v: string) => {
                this.fSubject = v;
              })
          }
          .alignItems(HorizontalAlign.Start)
          .layoutWeight(1)
          Column({ space: 3 }) {
            Text('建议分值').fontSize(11).fontColor(OgC.INK_SUB).width('100%')
            TextInput({ text: this.fScore, placeholder: '如 2' })
              .type(InputType.NUMBER_DECIMAL)
              .fontSize(14).height(38).width('100%')
              .onChange((v: string) => {
                this.fScore = v;
              })
          }
          .alignItems(HorizontalAlign.Start)
          .layoutWeight(1)
        }
        .width('100%')
        Row({ space: 6 }) {
          ForEach(KINDS, (k: string) => {
            Text(OgFormat.kindName(k))
              .fontSize(12)
              .fontColor(this.fKind === k ? Color.White : OgC.INK)
              .backgroundColor(this.fKind === k ? OgC.BRAND : OgC.BG)
              .padding({ left: 10, right: 10, top: 6, bottom: 6 })
              .borderRadius(12)
              .onClick(() => {
                this.fKind = k;
              })
          }, (k: string) => k)
        }
        .width('100%')
        if (this.fKind === 'choice' || this.fKind === 'multiple') {
          TextInput({ text: this.fOptions, placeholder: '选项数(2~6)' })
            .type(InputType.Number)
            .fontSize(14).height(38).width('100%')
            .onChange((v: string) => {
              this.fOptions = v;
            })
        }
        TextArea({ text: this.fStem, placeholder: '题干 *(可后续补题干截图)' })
          .fontSize(13)
          .height(72)
          .onChange((v: string) => {
            this.fStem = v;
          })
        TextInput({ text: this.fAnswer, placeholder: '参考答案(客观题为选项字母,如 A / AC)' })
          .fontSize(14).height(38).width('100%')
          .onChange((v: string) => {
            this.fAnswer = v;
          })
        TextInput({ text: this.fKnowledge, placeholder: '知识点(多个用 , 、 分隔)' })
          .fontSize(14).height(38).width('100%')
          .onChange((v: string) => {
            this.fKnowledge = v;
          })
        Row({ space: 6 }) {
          ForEach(DIFFS, (d: string) => {
            Text(d)
              .fontSize(12)
              .fontColor(this.fDifficulty === d ? Color.White : OgC.INK)
              .backgroundColor(this.fDifficulty === d ? OgC.WARN : OgC.BG)
              .padding({ left: 10, right: 10, top: 6, bottom: 6 })
              .borderRadius(12)
              .onClick(() => {
                this.fDifficulty = this.fDifficulty === d ? '' : d;
              })
          }, (d: string) => d + String(this.fDifficulty === d))
        }
        .width('100%')
        if (this.editId !== '') {
          Row({ space: 8 }) {
            PrimaryButton({ label: '📷 拍题干', usable: !this.busy, onTap: () => this.pickStem(false) })
            PrimaryButton({ label: '🖼 相册', fill: true, usable: !this.busy, onTap: () => this.pickStem(true) })
          }
          .width('100%')
          if (this.stemPickedName !== '') {
            Text(this.stemPickedName).fontSize(11).fontColor(OgC.GOOD).width('100%')
          }
        }
        PrimaryButton({
          full: true,
          usable: !this.busy,
          label: this.busy ? '保存中…' : '保存',
          onTap: () => this.saveForm()
        })
        Text('').height(10)
      }
      .padding(OgDim.PAD)
      .width('100%')
    }
    .height(480)
  }
}

function fsListNames(dir: string): string[] {
  try {
    return fs.listFileSync(dir);
  } catch (e) {
    return [];
  }
}
