// 带鉴权的网络图片组件:OgNet 下载 → 解码 PixelMap(走 ImageCache)→ Image 渲染。
// ArkUI Image 无法为网络请求附带 Authorization 头,切片/整卷等鉴权图一律走本组件。

import { image } from '@kit.ImageKit';
import { ImageCache, OgImageCodec, OgError } from '../common/OgNet';
import { OgSession } from '../common/OgSession';
import { OgC } from '../common/OgTheme';

@Component
export struct OgImage {
  @Prop @Watch('onUrl') url: string = '';
  @Prop maxDim: number = 2000;
  @Prop showRetry: boolean = true;
  @State pm: image.PixelMap | null = null;
  @State loading: boolean = false;
  @State failed: boolean = false;
  @State failMsg: string = '';

  onUrl() {
    this.load();
  }

  aboutToAppear(): void {
    this.load();
  }

  load(): void {
    if (this.url === '') {
      this.pm = null;
      this.failed = true;
      this.failMsg = '无图像';
      return;
    }
    const cached = ImageCache.get(this.url);
    if (cached !== null) {
      this.pm = cached;
      this.loading = false;
      this.failed = false;
      return;
    }
    const target = this.url;
    this.loading = true;
    this.failed = false;
    OgImageCodec.decodeUrl(target, OgSession.imageCap(this.maxDim))
      .then((pm: image.PixelMap) => {
        ImageCache.put(target, pm);
        if (this.url === target) {
          this.pm = pm;
          this.loading = false;
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

  build() {
    Stack() {
      if (this.pm !== null) {
        Image(this.pm)
          .objectFit(ImageFit.Contain)
          .width('100%')
          .height('100%')
          .backgroundColor(Color.White)
      } else if (this.loading) {
        Column({ space: 8 }) {
          LoadingProgress().width(32).height(32).color(OgC.BRAND)
          Text('加载中…').fontSize(12).fontColor(OgC.INK_SUB)
        }
        .width('100%')
        .height('100%')
        .justifyContent(FlexAlign.Center)
        .alignItems(HorizontalAlign.Center)
        .backgroundColor(OgC.BG)
      } else {
        Column({ space: 8 }) {
          Text(this.failMsg).fontSize(12).fontColor(OgC.INK_SUB).textAlign(TextAlign.Center)
          if (this.showRetry) {
            Text('点按重试')
              .fontSize(12)
              .fontColor(OgC.BRAND)
              .onClick(() => this.load())
          }
        }
        .width('100%')
        .height('100%')
        .justifyContent(FlexAlign.Center)
        .alignItems(HorizontalAlign.Center)
        .backgroundColor(OgC.BG)
      }
    }
    .width('100%')
    .height('100%')
  }
}
