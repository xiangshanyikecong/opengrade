// 学生端错题重练卡:展示重练范围,并可直接保存可打印 SVG(服务端按本人错题重建版式)。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { PracticeResult } from '../../model/Api';
import { ErrorView, KVRow, LoadingView } from '../../components/Ui';

interface PracticeParams {
  id: string;
  child: string;
}

@Entry
@Component
struct PracticePage {
  @State examId: string = '';
  @State child: string = '';
  @State r: PracticeResult | null = null;
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State saving: boolean = false;

  aboutToAppear(): void {
    const p = router.getParams() as PracticeParams | null;
    if (p !== null && p !== undefined && p.id !== undefined) {
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

  private async load(): Promise<void> {
    try {
      this.r = await OgNet.jget<PracticeResult>('/api/v1/student/exams/' + this.examId + '/practice' + this.childQ());
      this.failed = false;
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '生成失败';
    }
    this.loading = false;
  }

  private async saveSvg(): Promise<void> {
    this.saving = true;
    try {
      const uri = await OgNet.downloadToPicker(
        '/api/v1/student/exams/' + this.examId + '/practice.svg' + this.childQ(), '错题重练卡.svg');
      promptAction.showToast({ message: uri === '' ? '已取消' : '已保存,可用系统打印服务打印再练' });
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '保存失败' });
    }
    this.saving = false;
  }

  build() {
    Column() {
      Row() {
        Text('‹').fontSize(26).fontColor(OgC.BRAND).width(36).height(36).textAlign(TextAlign.Center)
          .onClick(() => router.back())
        Text('错题重练卡').fontSize(17).fontWeight(FontWeight.Bold).fontColor(OgC.INK)
        Blank()
      }
      .width('100%')
      .padding(12)
      .backgroundColor(OgC.CARD)

      if (this.loading) {
        LoadingView()
      } else if (this.failed) {
        ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
      } else if (this.r !== null) {
        Column({ space: OgDim.GAP }) {
          Column({ space: 8 }) {
            Text(this.r.title).fontSize(15).fontWeight(FontWeight.Bold).fontColor(OgC.INK)
            KVRow({ k: '抽出的客观题', v: this.r.objective + ' 道' })
            KVRow({ k: '抽出的主观题', v: this.r.subjective + ' 道' })
            KVRow({ k: '涉及原卷题号', v: this.r.questions.join('、') })
          }
          .width('100%')
          .padding(14)
          .backgroundColor(OgC.CARD)
          .borderRadius(OgDim.RADIUS)
          .alignItems(HorizontalAlign.Start)

          Text('练习卡保留了原题的题型、分值与知识点标签,作答区留空;打印后再练,布局几何与正式答题卡同源,可继续被 OMR 识别批改。')
            .fontSize(12).fontColor(OgC.INK_SUB)

          Button(this.saving ? '保存中…' : '保存可打印卡片(SVG)')
            .width('100%')
            .height(46)
            .fontSize(15)
            .backgroundColor(OgC.BRAND)
            .enabled(!this.saving)
            .onClick(() => this.saveSvg())
          Text('保存后可在「文件」中找到,通过系统打印或分享给家长打印。')
            .fontSize(11).fontColor(OgC.INK_SUB)
        }
        .padding(OgDim.PAD)
        .width('100%')
        .alignItems(HorizontalAlign.Start)
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
  }
}
