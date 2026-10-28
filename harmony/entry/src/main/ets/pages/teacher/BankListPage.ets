// 校本题库(手机端只读):列表/搜索/题干缩略图;录入与编辑在电脑端操作。
// 数据契约:GET /api/v1/bank?subject=&kind=&q=;题干图 GET /api/v1/bank/{id}/stem。

import { router } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { BankQuestion } from '../../model/Api';
import { EmptyView, ErrorView, LoadingView, Pill, SectionHeader, TitleBar } from '../../components/Ui';
import { OgImage } from '../../components/OgImage';
import { StaffNavGrid } from '../../components/StaffNav';

@Entry
@Component
struct BankListPage {
  @State items: BankQuestion[] = [];
  @State q: string = '';
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
      this.items = await OgNet.jget<BankQuestion[]>('/api/v1/bank' + OgNet.qs({
        'subject': '', 'kind': '', 'q': this.q.trim()
      }));
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  @Builder
  navAction() {
    Text('☰').fontSize(20).fontColor(OgC.BRAND).padding({ left: 10, right: 4 })
      .onClick(() => {
        this.navOpen = true;
      })
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
      if (it.usedIn !== undefined && it.usedIn.length > 0) {
        Text('已被 ' + it.usedIn.length + ' 场考试使用').fontSize(10).fontColor(OgC.INK_SUB)
      }
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
            EmptyView({ text: '题库为空。在电脑端设计器「从题库选题」或试卷识别导入。' })
          }
          ForEach(this.items, (it: BankQuestion) => {
            this.bankCard(it)
          }, (it: BankQuestion) => it.id)
          Text('手机端题库为只读视图;录入、编辑、删除请在电脑端操作。')
            .fontSize(11).fontColor(OgC.INK_SUB)
            .width('100%')
            .textAlign(TextAlign.Center)
            .margin({ top: 8 })
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
      height: 360, dragBar: true, title: { title: '工作台导航' }
    })
  }

  @Builder
  sheetBuilder() {
    StaffNavGrid({ current: 'bank', onClose: () => {
      this.navOpen = false;
    } })
  }
}
