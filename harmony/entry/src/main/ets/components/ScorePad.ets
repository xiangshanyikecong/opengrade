// 触屏评分盘:对齐 web Grader 键盘语义 ——
//   满分 ≤10:数字键即打即存;>10:十位+个位两键合成(1→2 = 12 分);
//   ±0.5 半分修正、小数输入条、同上分、满分/零分快捷键。
// 保存与跳转策略由父页面(GraderPage)负责,本组件只产出"分数意图"。

import { OgC, OgDim } from '../common/OgTheme';
import { OgFormat } from '../common/OgFormat';

@Component
export struct ScorePad {
  @Prop cap: number = 10; // 当前题/当前小问满分
  @Prop lastScore: number = NaN; // 上一份的分数(同上分用)
  onScore: (v: number) => void = () => {};
  onNudge: (delta: number) => void = () => {};
  onSame: () => void = () => {};
  @State tens: number = 0; // 两键合成缓冲:0=未选十位
  @State inputBuf: string = '';

  private digitRows(): number[] {
    const out: number[] = [];
    const maxDigit = Math.min(9, Math.floor(this.cap));
    for (let i = 0; i <= maxDigit; i++) {
      out.push(i);
    }
    return out;
  }

  private tensRow(): number[] {
    const out: number[] = [];
    const maxTens = Math.max(1, Math.floor(this.cap / 10));
    for (let i = 1; i <= maxTens; i++) {
      out.push(i);
    }
    return out;
  }

  private unitsRow(): number[] {
    const out: number[] = [];
    for (let i = 0; i <= 9; i++) {
      out.push(i);
    }
    return out;
  }

  private tapDigit(d: number): void {
    if (this.cap <= 10) {
      this.tens = 0;
      this.onScore(d);
      return;
    }
    if (this.tens === 0) {
      if (d === 0) {
        this.onScore(0); // 与 web 一致:0 直接打零分
        return;
      }
      this.tens = d;
      return;
    }
    const v = this.tens * 10 + d;
    if (v > this.cap) {
      this.tens = 0;
      return; // 父页面以 toast 提示越界由 onScore 校验;这里静默复位
    }
    this.tens = 0;
    this.onScore(v);
  }

  private commitInput(): void {
    const v = parseFloat(this.inputBuf);
    if (isNaN(v)) {
      return;
    }
    this.inputBuf = '';
    this.onScore(v);
  }

  @Builder
  padBtn(label: string, sub: string, onTap: () => void, hot: boolean) {
    Column({ space: 1 }) {
      Text(label).fontSize(17).fontWeight(FontWeight.Bold).fontColor(hot ? Color.White : OgC.INK)
      if (sub !== '') {
        Text(sub).fontSize(9).fontColor(hot ? '#E8FFF8' : OgC.INK_SUB)
      }
    }
    .justifyContent(FlexAlign.Center)
    .layoutWeight(1)
    .height(46)
    .backgroundColor(hot ? OgC.BRAND : OgC.CARD)
    .borderRadius(8)
    .borderWidth(1)
    .borderColor(hot ? OgC.BRAND : OgC.LINE)
    .onClick(() => onTap())
  }

  build() {
    Column({ space: 8 }) {
      if (this.tens !== 0) {
        Text('已选十位 ' + this.tens + '_,点个位补全(满分 ' + OgFormat.score(this.cap) + ')')
          .fontSize(12)
          .fontColor(OgC.WARN)
          .width('100%')
          .textAlign(TextAlign.Center)
          .onClick(() => {
            this.tens = 0;
          })
      }
      if (this.cap <= 10) {
        Row({ space: 6 }) {
          ForEach(this.digitRows(), (d: number) => {
            this.padBtn(String(d), '', () => this.tapDigit(d), false)
          }, (d: number) => 'd' + d)
        }
        .width('100%')
      } else {
        Row({ space: 6 }) {
          ForEach(this.tensRow(), (d: number) => {
            this.padBtn(String(d), '十位', () => this.tapDigit(d), this.tens === d)
          }, (d: number) => 't' + d)
        }
        .width('100%')
        Row({ space: 6 }) {
          ForEach(this.unitsRow(), (d: number) => {
            this.padBtn(String(d), '', () => this.tapDigit(d), false)
          }, (d: number) => 'u' + d)
        }
        .width('100%')
      }
      Row({ space: 6 }) {
        this.padBtn('满分', OgFormat.score(this.cap), () => this.onScore(this.cap), true)
        this.padBtn('-0.5', '', () => this.onNudge(-0.5), false)
        this.padBtn('+0.5', '', () => this.onNudge(0.5), false)
        this.padBtn('同上分', isNaN(this.lastScore) ? '' : OgFormat.score(this.lastScore), () => this.onSame(), false)
      }
      .width('100%')
      Row({ space: 6 }) {
        TextInput({ text: this.inputBuf, placeholder: '小数/两位数直录,回车确认' })
          .type(InputType.NUMBER_DECIMAL)
          .fontSize(14)
          .height(40)
          .layoutWeight(1)
          .onChange((v: string) => {
            this.inputBuf = v;
          })
          .onSubmit(() => this.commitInput())
        Button('确认')
          .fontSize(14)
          .backgroundColor(OgC.BRAND_DEEP)
          .height(40)
          .onClick(() => this.commitInput())
      }
      .width('100%')
    }
    .width('100%')
    .padding(10)
    .backgroundColor(OgC.BG)
    .borderRadius(OgDim.RADIUS)
  }
}
