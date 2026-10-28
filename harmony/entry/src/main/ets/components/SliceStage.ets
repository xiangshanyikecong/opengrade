// 切片舞台:鉴权切片图 + 归一化笔迹叠加 + 灰度/反色增强 + 全屏细看。
// 画布与图片同尺寸(按解码后宽高比计算),保证笔迹坐标归一化正确。

import { image } from '@kit.ImageKit';
import { router } from '@kit.ArkUI';
import { ImageCache, OgImageCodec, OgError } from '../common/OgNet';
import { OgC } from '../common/OgTheme';
import { Stroke } from '../model/Api';
import { InkCanvas } from './InkCanvas';

const GRAY_FILTER: number[] = [
  0.33, 0.59, 0.11, 0, 0,
  0.33, 0.59, 0.11, 0, 0,
  0.33, 0.59, 0.11, 0, 0,
  0, 0, 0, 1, 0
];
const INVERT_FILTER: number[] = [
  -1, 0, 0, 0, 1,
  0, -1, 0, 0, 1,
  0, 0, -1, 0, 1,
  0, 0, 0, 1, 0
];

@Component
export struct SliceStage {
  @Prop @Watch('onUrl') url: string = '';
  @Prop @Watch('redrawInk') strokes: Stroke[] = [];
  @Prop inkOn: boolean = false;
  @Prop @Watch('redrawInk') enhance: number = 0; // 0 原图 | 1 灰度 | 2 反色
  @Prop missing: boolean = false;
  onStrokeDone: (s: Stroke) => void = () => {};
  @State pm: image.PixelMap | null = null;
  @State loading: boolean = false;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State stageW: number = 0;
  @State dispH: number = 200;
  private loadedKey: string = '';

  onUrl(): void {
    this.load();
  }

  aboutToAppear(): void {
    this.load();
  }

  load(): void {
    if (this.url === '') {
      this.pm = null;
      return;
    }
    const cached = ImageCache.get(this.url);
    if (cached !== null) {
      this.pm = cached;
      this.failed = false;
      this.loading = false;
      this.syncAspect(cached);
      return;
    }
    const target = this.url;
    this.loading = true;
    this.failed = false;
    OgImageCodec.decodeUrl(target, 2400)
      .then((pm: image.PixelMap) => {
        ImageCache.put(target, pm);
        if (this.url === target) {
          this.pm = pm;
          this.loading = false;
          this.syncAspect(pm);
        }
      })
      .catch((e: Error) => {
        if (this.url === target) {
          this.loading = false;
          this.failed = true;
          this.failMsg = e instanceof OgError ? e.message : '图像加载失败';
        }
      });
  }

  private syncAspect(pm: image.PixelMap): void {
    pm.getImageInfo().then((info) => {
      const w = info.size.width;
      const h = info.size.height;
      if (w > 0) {
        this.dispH = Math.round(this.stageW * h / w);
      }
    }).catch(() => {});
  }

  redrawInk(): void {
    // InkCanvas 自带 @Watch,这里仅在 stageW 变化时同步一次高度
  }

  private filterMatrix(): number[] | null {
    if (this.enhance === 1) {
      return GRAY_FILTER;
    }
    if (this.enhance === 2) {
      return INVERT_FILTER;
    }
    return null;
  }

  private openFull(): void {
    if (this.url === '') {
      return;
    }
    router.pushUrl({
      url: 'pages/common/ImageViewerPage',
      params: { 'url': this.url, 'title': '切片细看' } as Record<string, string>
    }).catch(() => {});
  }

  build() {
    Column() {
      if (this.missing) {
        Column({ space: 8 }) {
          Text('🖨').fontSize(30)
          Text('手工补录卷无切片图').fontSize(12).fontColor(OgC.INK_SUB)
        }
        .width('100%')
        .height(180)
        .justifyContent(FlexAlign.Center)
        .backgroundColor(OgC.CARD)
        .borderRadius(10)
      } else if (this.failed) {
        Column({ space: 8 }) {
          Text(this.failMsg).fontSize(12).fontColor(OgC.INK_SUB)
          Text('点按重试').fontSize(12).fontColor(OgC.BRAND).onClick(() => this.load())
        }
        .width('100%')
        .height(180)
        .justifyContent(FlexAlign.Center)
        .backgroundColor(OgC.CARD)
        .onClick(() => this.load())
      } else if (this.pm !== null && this.stageW > 0) {
        Stack({ alignContent: Alignment.TopStart }) {
          Image(this.pm)
            .width(this.stageW)
            .height(this.dispH)
            .objectFit(ImageFit.Fill)
            .colorFilter(this.filterMatrix())
          InkCanvas({
            strokes: this.strokes,
            inkOn: this.inkOn,
            stageW: this.stageW,
            stageH: this.dispH,
            onStrokeDone: (s: Stroke) => this.onStrokeDone(s)
          })
            .position({ x: 0, y: 0 })
        }
        .width(this.stageW)
        .height(this.dispH)
        .onClick(() => {
          if (!this.inkOn) {
            this.openFull();
          }
        })
      } else {
        Column({ space: 8 }) {
          LoadingProgress().width(32).height(32).color(OgC.BRAND)
          Text('切片加载中…').fontSize(12).fontColor(OgC.INK_SUB)
        }
        .width('100%')
        .height(180)
        .justifyContent(FlexAlign.Center)
        .onAreaChange((oldArea: Area, newArea: Area) => {
          const w = Number(newArea.width);
          if (w > 0 && w !== this.stageW) {
            this.stageW = w;
            if (this.pm !== null) {
              this.syncAspect(this.pm);
            }
          }
        })
      }
    }
    .width('100%')
    .onAreaChange((oldArea: Area, newArea: Area) => {
      const w = Number(newArea.width);
      if (w > 0 && w !== this.stageW) {
        this.stageW = w;
        if (this.pm !== null) {
          this.syncAspect(this.pm);
        }
      }
    })
  }
}
