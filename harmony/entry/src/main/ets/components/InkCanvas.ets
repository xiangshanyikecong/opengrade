// 归一化笔迹画板:与 web Grader 同一数据契约(Stroke{color,pts 0~1 归一化折线})。
// 画布尺寸由父组件(SliceStage)按图片显示区给定;触屏绘制,抬手回调整条笔迹。

import { OgC } from '../common/OgTheme';
import { Stroke } from '../model/Api';

@Component
export struct InkCanvas {
  @Prop @Watch('redraw') strokes: Stroke[] = [];
  @Prop inkOn: boolean = false;
  @Prop @Watch('redraw') stageW: number = 0;
  @Prop @Watch('redraw') stageH: number = 0;
  onStrokeDone: (s: Stroke) => void = () => {};
  private settings: RenderingContextSettings = new RenderingContextSettings(true);
  private ctx: CanvasRenderingContext2D = new CanvasRenderingContext2D(this.settings);
  private cur: Stroke | null = null;
  private ready: boolean = false;

  redraw(): void {
    if (this.ready) {
      this.paint();
    }
  }

  private lineWidth(): number {
    return Math.max(3, Math.round(this.stageW / 300));
  }

  private paint(): void {
    const ctx = this.ctx;
    const w = this.stageW;
    const h = this.stageH;
    if (w <= 0 || h <= 0) {
      return;
    }
    ctx.clearRect(0, 0, w, h);
    ctx.lineWidth = this.lineWidth();
    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';
    for (const st of this.strokes) {
      this.drawStroke(ctx, st, w, h);
    }
    if (this.cur !== null) {
      this.drawStroke(ctx, this.cur, w, h);
    }
  }

  private drawStroke(ctx: CanvasRenderingContext2D, st: Stroke, w: number, h: number): void {
    ctx.strokeStyle = st.color === 'green' ? OgC.GOOD : OgC.BAD;
    ctx.beginPath();
    const pts = st.pts;
    for (let i = 0; i + 1 < pts.length; i += 2) {
      const x = pts[i] * w;
      const y = pts[i + 1] * h;
      if (i === 0) {
        ctx.moveTo(x, y);
      } else {
        ctx.lineTo(x, y);
      }
    }
    ctx.stroke();
  }

  build() {
    Canvas(this.ctx)
      .width(this.stageW > 0 ? this.stageW : '100%')
      .height(this.stageH > 0 ? this.stageH : 1)
      .hitTestBehavior(this.inkOn ? HitTestMode.Default : HitTestMode.None)
      .onReady(() => {
        this.ready = true;
        this.paint();
      })
      .onTouch((event: TouchEvent) => {
        if (!this.inkOn || this.stageW <= 0) {
          return;
        }
        if (event.type === TouchType.Down) {
          const t = event.touches[0];
          this.cur = { color: 'red', pts: [t.x / this.stageW, t.y / this.stageH] };
        } else if (event.type === TouchType.Move && this.cur !== null) {
          const t = event.touches[0];
          this.cur.pts.push(t.x / this.stageW, t.y / this.stageH);
        } else if (event.type === TouchType.Up || event.type === TouchType.Cancel) {
          if (this.cur !== null && this.cur.pts.length >= 4) {
            const done = this.cur;
            this.onStrokeDone(done);
          }
          // 触屏滚动劫持导致的 cancel:丢弃未完成笔迹,避免悬挂(与 web 端同语义)
          this.cur = null;
          this.paint();
          return;
        }
        this.paint();
      })
  }
}
