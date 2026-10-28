// 全屏图片查看器:双指缩放 + 拖动 + 双击复位;支持多页(整卷回溯)翻页。
// 路由参数:{ url } 或 { urls: string[], index: number } + { title }。

import { router } from '@kit.ArkUI';
import { OgC } from '../../common/OgTheme';
import { OgImage } from '../../components/OgImage';

interface ViewerParams {
  url: string;
  urls: string[];
  index: number;
  title: string;
}

@Entry
@Component
struct ImageViewerPage {
  @State urls: string[] = [];
  @State index: number = 0;
  @State title: string = '查看';
  @State scale: number = 1;
  @State offsetX: number = 0;
  @State offsetY: number = 0;
  private lastScale: number = 1;

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
  }

  private resetTransform(): void {
    this.scale = 1;
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

  build() {
    Stack() {
      Column() {
        OgImage({ url: this.curUrl(), maxDim: 3000 })
      }
      .width('100%')
      .height('100%')
      .scale({ x: this.scale, y: this.scale })
      .translate({ x: this.offsetX, y: this.offsetY })
      .gesture(
        GestureGroup(GestureMode.Exclusive,
          PinchGesture({ fingers: 2 })
            .onActionStart(() => {
              this.lastScale = this.scale;
            })
            .onActionUpdate((event: GestureEvent) => {
              this.scale = Math.min(6, Math.max(1, this.lastScale * event.scale));
              if (this.scale <= 1.01) {
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
              if (this.scale > 1.01) {
                this.offsetX += event.offsetX;
                this.offsetY += event.offsetY;
              }
            })
        )
      )

      // 顶栏
      Row({ space: 10 }) {
        Text('‹')
          .fontSize(26).fontColor(Color.White).width(36).height(36).textAlign(TextAlign.Center)
          .onClick(() => router.back())
        Text(this.title).fontSize(15).fontColor(Color.White).layoutWeight(1)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
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
