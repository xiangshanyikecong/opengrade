// Canvas 自绘图表:折线(趋势)、柱状(分数段)、横条(知识点掌握度)。
// 绘制口径对齐 web/common.js 的 chartLine:左侧留白 46、底部留白 30、空值断线。

import { OgC } from '../common/OgTheme';

export interface LineSeries {
  name: string;
  color: string;
  values: number[]; // NaN 表示断线(如缺考)
}

@Component
export struct LineChart {
  @Prop @Watch('redraw') labels: string[] = [];
  @Prop @Watch('redraw') series: LineSeries[] = [];
  @Prop asRate: boolean = false; // y 值为 0~1 得分率
  @Prop chartHeight: number = 210;
  private settings: RenderingContextSettings = new RenderingContextSettings(true);
  private ctx: CanvasRenderingContext2D = new CanvasRenderingContext2D(this.settings);
  private ready: boolean = false;

  redraw(): void {
    if (this.ready) {
      this.draw();
    }
  }

  private fmtY(v: number): string {
    if (this.asRate) {
      return Math.round(v * 100) + '%';
    }
    return String(Math.round(v * 10) / 10);
  }

  private draw(): void {
    const ctx = this.ctx;
    const W = ctx.width;
    const H = ctx.height;
    if (W <= 0 || H <= 0) {
      return;
    }
    ctx.clearRect(0, 0, W, H);
    const padL = 46;
    const padR = 12;
    const padT = 14;
    const padB = 28;
    const plotW = W - padL - padR;
    const plotH = H - padT - padB;
    if (plotW <= 10 || plotH <= 10) {
      return;
    }
    const n = this.labels.length;
    // y 范围
    let yMin = 0;
    let yMax = 1;
    let hasData = false;
    for (const s of this.series) {
      for (const v of s.values) {
        if (!isNaN(v)) {
          hasData = true;
          if (this.asRate) {
            if (v > yMax) {
              yMax = v;
            }
          } else {
            if (yMax === 1 && v > 1) {
              yMax = v;
            } else if (v > yMax) {
              yMax = v;
            }
          }
        }
      }
    }
    if (!hasData) {
      ctx.fillStyle = OgC.INK_SUB;
      ctx.font = '13px sans-serif';
      ctx.textAlign = 'center';
      ctx.fillText('暂无数据', W / 2, H / 2);
      return;
    }
    if (!this.asRate) {
      yMax = Math.max(yMax, 1);
    }
    if (!this.asRate && yMax <= 10) {
      yMax = Math.min(yMax + 1, Math.ceil(yMax) + 1);
    }
    const span = yMax - yMin;
    if (span <= 0) {
      yMax = yMin + 1;
    }
    const xAt = (i: number): number => {
      if (n <= 1) {
        return padL + plotW / 2;
      }
      return padL + (plotW * i) / (n - 1);
    };
    const yAt = (v: number): number => {
      return padT + plotH * (1 - (v - yMin) / (yMax - yMin));
    };
    // 网格与 y 刻度
    ctx.strokeStyle = OgC.LINE;
    ctx.fillStyle = OgC.INK_SUB;
    ctx.lineWidth = 1;
    ctx.font = '10px sans-serif';
    ctx.textAlign = 'right';
    const ticks = 4;
    for (let t = 0; t <= ticks; t++) {
      const v = yMin + ((yMax - yMin) * t) / ticks;
      const y = yAt(v);
      ctx.beginPath();
      ctx.moveTo(padL, y);
      ctx.lineTo(W - padR, y);
      ctx.stroke();
      ctx.fillText(this.fmtY(v), padL - 6, y + 4);
    }
    // x 标签(最多 8 个)
    ctx.textAlign = 'center';
    const step = Math.max(1, Math.ceil(n / 8));
    for (let i = 0; i < n; i += step) {
      ctx.fillText(this.labels[i], xAt(i), H - 10);
    }
    // 折线
    for (const s of this.series) {
      ctx.strokeStyle = s.color;
      ctx.lineWidth = 2;
      ctx.beginPath();
      let drawing = false;
      for (let i = 0; i < n; i++) {
        const v = s.values[i];
        if (isNaN(v)) {
          drawing = false;
          continue;
        }
        const x = xAt(i);
        const y = yAt(v);
        if (!drawing) {
          ctx.moveTo(x, y);
          drawing = true;
        } else {
          ctx.lineTo(x, y);
        }
      }
      ctx.stroke();
      // 数据点(序列少时)
      if (n <= 20) {
        for (let i = 0; i < n; i++) {
          const v = s.values[i];
          if (isNaN(v)) {
            continue;
          }
          ctx.beginPath();
          ctx.fillStyle = s.color;
          ctx.arc(xAt(i), yAt(v), 2.5, 0, Math.PI * 2);
          ctx.fill();
        }
      }
    }
  }

  build() {
    Column({ space: 6 }) {
      Canvas(this.ctx)
        .width('100%')
        .height(this.chartHeight)
        .onReady(() => {
          this.ready = true;
          this.draw();
        })
      // 图例
      Flex({ wrap: FlexWrap.Wrap, justifyContent: FlexAlign.Center }) {
        ForEach(this.series, (s: LineSeries) => {
          Row({ space: 4 }) {
            Row().width(8).height(8).backgroundColor(s.color).borderRadius(4)
            Text(s.name).fontSize(11).fontColor(OgC.INK_SUB)
          }.margin({ right: 10, bottom: 2 })
        }, (s: LineSeries) => s.name)
      }
      .width('100%')
    }
    .width('100%')
  }
}

// 分数段柱状图(学情分析)
@Component
export struct BarChart {
  @Prop @Watch('redraw') labels: string[] = [];
  @Prop @Watch('redraw') values: number[] = [];
  @Prop color: string = OgC.BRAND;
  @Prop chartHeight: number = 180;
  private settings: RenderingContextSettings = new RenderingContextSettings(true);
  private ctx: CanvasRenderingContext2D = new CanvasRenderingContext2D(this.settings);
  private ready: boolean = false;

  redraw(): void {
    if (this.ready) {
      this.draw();
    }
  }

  private draw(): void {
    const ctx = this.ctx;
    const W = ctx.width;
    const H = ctx.height;
    ctx.clearRect(0, 0, W, H);
    const n = this.values.length;
    if (n === 0) {
      return;
    }
    const padL = 30;
    const padB = 26;
    const padT = 18;
    const plotW = W - padL - 8;
    const plotH = H - padT - padB;
    let maxV = 1;
    for (const v of this.values) {
      if (v > maxV) {
        maxV = v;
      }
    }
    const barW = (plotW / n) * 0.62;
    ctx.fillStyle = OgC.INK_SUB;
    ctx.font = '10px sans-serif';
    ctx.textAlign = 'right';
    for (let t = 0; t <= 3; t++) {
      const v = (maxV * t) / 3;
      const y = padT + plotH * (1 - t / 3);
      ctx.strokeStyle = OgC.LINE;
      ctx.beginPath();
      ctx.moveTo(padL, y);
      ctx.lineTo(W - 8, y);
      ctx.stroke();
      ctx.fillText(String(Math.round(v)), padL - 4, y + 4);
    }
    ctx.textAlign = 'center';
    for (let i = 0; i < n; i++) {
      const cx = padL + (plotW * (i + 0.5)) / n;
      const bh = (this.values[i] / maxV) * plotH;
      ctx.fillStyle = this.color;
      ctx.fillRect(cx - barW / 2, padT + plotH - bh, barW, bh);
      ctx.fillStyle = OgC.INK_SUB;
      ctx.fillText(this.labels[i], cx, H - 8);
      if (this.values[i] > 0) {
        ctx.fillText(String(this.values[i]), cx, padT + plotH - bh - 4);
      }
    }
  }

  build() {
    Canvas(this.ctx)
      .width('100%')
      .height(this.chartHeight)
      .onReady(() => {
        this.ready = true;
        this.draw();
      })
  }
}

export interface HBarItem {
  name: string;
  rate: number; // 0~1
  extra: string; // 右侧说明(如 "3题 · 62%")
}

// 知识点掌握度横条(薄弱排前)
@Component
export struct HBarList {
  @Prop items: HBarItem[] = [];
  @Prop sortAsc: boolean = true;
  @Prop barHeight: number = 26;

  private sorted(): HBarItem[] {
    const arr: HBarItem[] = [];
    for (const it of this.items) {
      arr.push(it);
    }
    if (this.sortAsc) {
      arr.sort((a, b) => a.rate - b.rate);
    }
    return arr;
  }

  build() {
    Column({ space: 6 }) {
      if (this.items.length === 0) {
        Text('暂无知识点数据(可在建考/详情页为题目补录知识点标签)')
          .fontSize(12).fontColor(OgC.INK_SUB).padding(10)
      }
      ForEach(this.sorted(), (it: HBarItem) => {
        Row({ space: 8 }) {
          Text(it.name)
            .fontSize(12)
            .fontColor(OgC.INK)
            .width('34%')
            .maxLines(1)
            .textOverflow({ overflow: TextOverflow.Ellipsis })
          Stack({ alignContent: Alignment.Start }) {
            Row()
              .width('100%')
              .height(this.barHeight - 12)
              .backgroundColor(OgC.BG)
              .borderRadius(6)
            Row()
              .width(Math.max(4, it.rate * 100) + '%')
              .height(this.barHeight - 12)
              .backgroundColor(it.rate < 0.6 ? OgC.BAD : (it.rate < 0.8 ? OgC.WARN : OgC.GOOD))
              .borderRadius(6)
          }
          .layoutWeight(1)
          Text(it.extra)
            .fontSize(11)
            .fontColor(OgC.INK_SUB)
            .width('26%')
            .textAlign(TextAlign.End)
            .maxLines(1)
        }
        .width('100%')
      }, (it: HBarItem) => it.name)
    }
    .width('100%')
  }
}
