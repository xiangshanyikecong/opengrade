// 全屏图片查看器:双指缩放 + 拖动 + 双击复位;支持多页(整卷回溯)翻页。
// 传入 strokesJson(阅卷切片笔迹)时以「图 + 笔迹画布」同层缩放渲染(阅卷放大镜):
//   捏合放大后笔迹与墨迹同步缩放,逐点核对小字作答。
// 路由参数:{ url } 或 { urls: string[], index: number } + { title } + { strokesJson? }。

import { router } from '@kit.ArkUI';
import { image } from '@kit.ImageKit';
import { ImageCache, OgImageCodec, OgError } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
import { OgC } from '../../common/OgTheme';
import { Stroke } from '../../model/Api';
import { InkCanvas } from '../../components/InkCanvas';
import { OgImage } from '../../components/OgImage';

interface ViewerParams {
  url: string;
  urls: string[];
  index: number;
  title: string;
  strokesJson: string;
}

@Entry
@Component
struct ImageViewerPage {
  @State urls: string[] = [];
  @State index: number = 0;
  @State title: string = '查看';
  @State strokes: Stroke[] = [];
  @State zoomScale: number = 1;
  @State offsetX: number = 0;
  @State offsetY: number = 0;
  // 笔迹模式:图 + InkCanvas 同层(画布与图片同尺寸保证归一化坐标对齐)
  @State inkPm: image.PixelMap | null = null;
  @State imgW: number = 0;
  @State imgH: number = 0;
  @State areaW: number = 0;
  @State areaH: number = 0;
  @State inkFailed: boolean = false;
  private lastScale: number = 1;
  private loadedInkUrl: string = '';

  aboutToAppear(): void {
    const p = router.getParams() as ViewerParams | null;
    if (p === null) {
      return;
    }
    if (p.urls !== undefined && p.urls !== null && p.urls.length > 0) {
      this.urls = p.urls;
      this.index = p.index ?? 0;
    } else if (p.url !== undefined) {
      this.urls = [p.url];
      this.index = 0;
    }
    if (p.title !== undefined) {
      this.title = p.title;
    }
    if (p.strokesJson !== undefined && p.strokesJson !== '') {
      try {
        const arr = JSON.parse(p.strokesJson) as Stroke[];
        this.strokes = arr ?? [];
      } catch (e) {
        this.strokes = [];
      }
    }
    if (this.strokes.length > 0) {
      this.loadInk(this.curUrl());
    }
  }

  private resetTransform(): void {
    this.zoomScale = 1;
    this.offsetX = 0;
    this.offsetY = 0;
    this.lastScale = 1;
  }

  private curUrl(): string {
    if (this.urls.length === 0) {
      return '';
    }
    return this.urls[this.index];
  }

  // 笔迹模式换页:重载对应切片图。
  private loadInk(url: string): void {
    this.inkPm = null;
    this.inkFailed = false;
    this.loadedInkUrl = url;
    const target = url;
    const cached = ImageCache.get(url);
    if (cached !== null) {
      this.inkPm = cached;
      this.syncInkAspect(cached);
      return;
    }
    OgImageCodec.decodeUrl(target, OgSession.imageCap(2600))
      .then((pm: image.PixelMap) => {
        ImageCache.put(target, pm);
        if (this.loadedInkUrl === target) {
          this.inkPm = pm;
          this.syncInkAspect(pm);
        }
      })
      .catch((e: Error) => {
        if (this.loadedInkUrl === target) {
          this.inkFailed = e instanceof OgError ? e.message : '图像加载失败';
        }
      });
  }

  private syncInkAspect(pm: image.PixelMap): void {
    pm.getImageInfo().then((info) => {
      const iw = info.size.width;
      const ih = info.size.height;
      if (iw > 0 && ih > 0 && this.areaW > 0 && this.areaH > 0) {
        // contain 适配:整图可见,放大再捏合
        const k = Math.min(this.areaW / iw, this.areaH / ih);
        this.imgW = Math.round(iw * k);
        this.imgH = Math.round(ih * k);
      }
    }).catch(() => {});
  }

  build() {
    Stack() {
      if (this.strokes.length > 0) {
        // 阅卷放大镜:图 + 笔迹画布同层缩放拖动
        Column() {
          if (this.inkPm !== null && this.imgW > 0 && this.imgH > 0) {
            Stack({ alignContent: Alignment.TopStart }) {
              Image(this.inkPm)
                .width(this.imgW)
                .height(this.imgH)
                .objectFit(ImageFit.Fill)
              InkCanvas({
                strokes: this.strokes,
                inkOn: false,
                stageW: this.imgW,
                stageH: this.imgH
              })
                .position({ x: 0, y: 0 })
            }
            .width(this.imgW)
            .height(this.imgH)
          } else if (this.inkFailed) {
            Column({ space: 8 }) {
              Text(this.inkFailed).fontSize(13).fontColor(Color.White)
              Text('点按重试').fontSize(13).fontColor(OgC.BRAND).onClick(() => this.loadInk(this.curUrl()))
            }
            .width('100%')
            .height('100%')
            .justifyContent(FlexAlign.Center)
          } else {
            LoadingProgress().width(34).height(34).color(OgC.BRAND)
          }
        }
        .width('100%')
        .height('100%')
        .justifyContent(FlexAlign.Center)
        .alignItems(HorizontalAlign.Center)
        .scale({ x: this.zoomScale, y: this.zoomScale })
        .translate({ x: this.offsetX, y: this.offsetY })
        .onAreaChange((oldArea: Area, newArea: Area) => {
          const w = Number(newArea.width);
          const h = Number(newArea.height);
          if (w > 0 && (w !== this.areaW || h !== this.areaH)) {
            this.areaW = w;
            this.areaH = h;
            if (this.inkPm !== null) {
              this.syncInkAspect(this.inkPm);
            }
          }
        })
        .gesture(
          GestureGroup(GestureMode.Exclusive,
            PinchGesture({ fingers: 2 })
              .onActionStart(() => {
                this.lastScale = this.zoomScale;
              })
              .onActionUpdate((event: GestureEvent) => {
                this.zoomScale = Math.min(6, Math.max(1, this.lastScale * event.scale));
                if (this.zoomScale <= 1.01) {
                  this.offsetX = 0;
                  this.offsetY = 0;
                }
              }),
            TapGesture({ count: 2 })
              .onAction(() => {
                this.resetTransform();
              }),
            PanGesture({ fingers: 1 })
              .onActionUpdate((event: GestureEvent) => {
                if (this.zoomScale > 1.01) {
                  this.offsetX += event.offsetX;
                  this.offsetY += event.offsetY;
                }
              })
          )
        )
      } else {
        Column() {
          OrdinaryViewer({ url: this.curUrl() })
        }
        .width('100%')
        .height('100%')
        .scale({ x: this.zoomScale, y: this.zoomScale })
        .translate({ x: this.offsetX, y: this.offsetY })
        .gesture(
          GestureGroup(GestureMode.Exclusive,
            PinchGesture({ fingers: 2 })
              .onActionStart(() => {
                this.lastScale = this.zoomScale;
              })
              .onActionUpdate((event: GestureEvent) => {
                this.zoomScale = Math.min(6, Math.max(1, this.lastScale * event.scale));
                if (this.zoomScale <= 1.01) {
                  this.offsetX = 0;
                  this.offsetY = 0;
                }
              }),
            TapGesture({ count: 2 })
              .onAction(() => {
                this.resetTransform();
              }),
            PanGesture({ fingers: 1 })
              .onActionUpdate((event: GestureEvent) => {
                if (this.zoomScale > 1.01) {
                  this.offsetX += event.offsetX;
                  this.offsetY += event.offsetY;
                }
              })
          )
        )
      }

      // 顶栏
      Row({ space: 10 }) {
        Text('‹')
          .fontSize(26).fontColor(Color.White).width(36).height(36).textAlign(TextAlign.Center)
          .onClick(() => router.back())
        Text(this.title).fontSize(15).fontColor(Color.White).layoutWeight(1)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        if (this.strokes.length > 0) {
          Text('🔍 捏合放大 · 双击复位').fontSize(11).fontColor(Color.White)
        }
        if (this.urls.length > 1) {
          Text((this.index + 1) + '/' + this.urls.length).fontSize(13).fontColor(Color.White)
        }
      }
      .width('100%')
      .padding({ left: 10, right: 14, top: 10 })
      .alignItems(VerticalAlign.Center)

      if (this.urls.length > 1) {
        Row({ space: 16 }) {
          Text('‹ 上一页')
            .fontSize(14).fontColor(this.index > 0 ? Color.White : '#66FFFFFF')
            .padding(10)
            .onClick(() => {
              if (this.index > 0) {
                this.index--;
                this.resetTransform();
                if (this.strokes.length > 0) {
                  this.loadInk(this.curUrl());
                }
              }
            })
          Blank()
          Text('下一页 ›')
            .fontSize(14).fontColor(this.index < this.urls.length - 1 ? Color.White : '#66FFFFFF')
            .padding(10)
            .onClick(() => {
              if (this.index < this.urls.length - 1) {
                this.index++;
                this.resetTransform();
                if (this.strokes.length > 0) {
                  this.loadInk(this.curUrl());
                }
              }
            })
        }
        .width('100%')
        .padding({ left: 16, right: 16, bottom: 24 })
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor(Color.Black)
  }
}

// 无笔迹场景的渲染(带鉴权图,Contain 适配)。
@Component
struct OrdinaryViewer {
  @Prop url: string = '';

  build() {
    OgImage({ url: this.url, maxDim: 3000 })
  }
}
