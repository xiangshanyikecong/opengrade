// PDF 查看器:pdfService 逐页渲染为位图 + 前后翻页 + 系统分享 + 另存为。
// 路由参数:{ url }(API 路径,自动下载到沙箱)或 { path }(沙箱内已有文件)
//          + { title } + { fileName }(分享/另存用文件名)。
// 用途:学生/家长成绩单 report.pdf、教师端全班成绩条 slips.pdf。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { picker, fileIo as fs } from '@kit.CoreFileKit';
import { image } from '@kit.ImageKit';
import { pdfService } from '@kit.PDFKit';
import { OgNet, OgError } from '../../common/OgNet';
import { OgShare } from '../../common/OgShare';
import { OgSession } from '../../common/OgSession';
import { OgC } from '../../common/OgTheme';
import { LoadingView, PrimaryButton } from '../../components/Ui';

interface PdfParams {
  url: string;
  path: string;
  title: string;
  fileName: string;
}

@Entry
@Component
struct PdfViewerPage {
  @State title: string = 'PDF';
  @State fileName: string = 'document.pdf';
  @State pm: image.PixelMap | null = null;
  @State pageCount: number = 0;
  @State cur: number = 0;
  @State loading: boolean = true;
  @State failed: string = '';
  @State shareBusy: boolean = false;
  private doc: pdfService.PdfDocument | null = null;
  private sandboxPath: string = '';
  private lastRendered: number = -1;

  aboutToAppear(): void {
    const p = router.getParams() as PdfParams | null;
    if (p !== null) {
      if (p.title !== undefined && p.title !== '') {
        this.title = p.title;
      }
      if (p.fileName !== undefined && p.fileName !== '') {
        this.fileName = p.fileName;
      }
      this.open(p.url ?? '', p.path ?? '');
    } else {
      this.failed = '缺少参数';
      this.loading = false;
    }
  }

  aboutToDisappear(): void {
    this.releaseDoc();
  }

  private releaseDoc(): void {
    if (this.doc !== null) {
      try {
        this.doc.releaseDocument();
      } catch (e) {
        // 忽略
      }
      this.doc = null;
    }
  }

  private async open(url: string, path: string): Promise<void> {
    this.loading = true;
    try {
      if (path !== '') {
        this.sandboxPath = path;
      } else {
        this.sandboxPath = OgSession.context().cacheDir + '/pdfview_' + Date.now() + '.pdf';
        const buf = await OgNet.getBytes(url, 600000);
        const f = fs.openSync(this.sandboxPath,
          fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
        try {
          fs.writeSync(f.fd, buf);
        } finally {
          fs.closeSync(f.fd);
        }
      }
      const doc = new pdfService.PdfDocument();
      const loadResult = doc.loadDocument(this.sandboxPath, '');
      if (loadResult !== pdfService.ParseResult.PARSE_SUCCESS) {
        throw new OgError(0, '无法解析该 PDF(可能已加密或损坏)');
      }
      this.doc = doc;
      this.pageCount = doc.getPageCount();
      if (this.pageCount <= 0) {
        throw new OgError(0, 'PDF 没有页面');
      }
      await this.render(0);
    } catch (e) {
      this.failed = e instanceof OgError ? e.message : 'PDF 加载失败';
    }
    this.loading = false;
  }

  // 渲染当前页:整页按宽 1240px 出图(≈150dpi,阅读清晰且内存可控)。
  private async render(index: number): Promise<void> {
    if (this.doc === null || index < 0 || index >= this.pageCount) {
      return;
    }
    this.loading = true;
    try {
      const page = this.doc.getPage(index);
      try {
        const w = page.getWidth();
        const h = page.getHeight();
        const matrix: pdfService.PdfMatrix = { x: 0, y: 0, width: w, height: h, rotate: 0 };
        const target = 1240;
        const bw = w >= h ? target : Math.max(320, Math.round(target * w / h));
        const bh = w >= h ? Math.max(320, Math.round(target * h / w)) : target;
        const npm = page.getAreaPixelMap(matrix, bw, bh, false, false);
        if (this.pm !== null) {
          try {
            this.pm.release();
          } catch (e) {
            // 忽略
          }
        }
        this.pm = npm;
        this.cur = index;
        this.lastRendered = index;
        this.failed = '';
      } finally {
        try {
          page.release();
        } catch (e) {
          // 忽略
        }
      }
    } catch (e) {
      this.failed = '第 ' + (index + 1) + ' 页渲染失败';
    }
    this.loading = false;
  }

  private async doShare(): Promise<void> {
    if (this.sandboxPath === '' || this.shareBusy) {
      return;
    }
    this.shareBusy = true;
    try {
      await OgShare.shareFile(this.sandboxPath, this.title);
    } catch (e) {
      promptAction.showToast({ message: '分享失败或已取消' });
    }
    this.shareBusy = false;
  }

  private async doSave(): Promise<void> {
    if (this.sandboxPath === '') {
      return;
    }
    try {
      const dp = new picker.DocumentViewPicker();
      const opt: picker.DocumentSaveOptions = { newFileNames: [this.fileName] };
      const uris: string[] = await dp.save(opt);
      if (uris.length === 0) {
        return;
      }
      const buf = this.readFileBytes();
      if (buf === null) {
        return;
      }
      const f = fs.openSync(uris[0], fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
      try {
        fs.writeSync(f.fd, buf);
      } finally {
        fs.closeSync(f.fd);
      }
      promptAction.showToast({ message: '已保存,可在「文件」中查看打印' });
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '保存失败' });
    }
  }

  private readFileBytes(): ArrayBuffer | null {
    try {
      const st = fs.statSync(this.sandboxPath);
      const buf = new ArrayBuffer(st.size);
      const f = fs.openSync(this.sandboxPath, fs.OpenMode.READ_ONLY);
      try {
        fs.readSync(f.fd, buf);
      } finally {
        fs.closeSync(f.fd);
      }
      return buf;
    } catch (e) {
      return null;
    }
  }

  build() {
    Column() {
      // 顶栏
      Row({ space: 10 }) {
        Text('‹').fontSize(26).fontColor(OgC.BRAND).width(36).height(36).textAlign(TextAlign.Center)
          .onClick(() => router.back())
        Column({ space: 1 }) {
          Text(this.title).fontSize(15).fontWeight(FontWeight.Bold).fontColor(OgC.INK)
            .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
          Text('第 ' + (this.cur + 1) + '/' + this.pageCount + ' 页').fontSize(11).fontColor(OgC.INK_SUB)
        }
        .alignItems(HorizontalAlign.Start)
        .layoutWeight(1)
        Text('分享').fontSize(13).fontColor(OgC.BRAND)
          .enabled(this.sandboxPath !== '' && !this.shareBusy)
          .opacity(this.shareBusy ? 0.5 : 1)
          .onClick(() => this.doShare())
        Text('另存').fontSize(13).fontColor(OgC.BRAND).padding({ left: 8 })
          .onClick(() => this.doSave())
      }
      .width('100%')
      .padding({ left: 8, right: 14, top: 8, bottom: 8 })
      .backgroundColor(OgC.CARD)

      if (this.loading && this.pm === null) {
        LoadingView({ text: '正在打开 PDF…' })
      } else if (this.failed !== '' && this.pm === null) {
        Column({ space: 10 }) {
          Text('⚠ ' + this.failed).fontSize(13).fontColor(OgC.BAD).textAlign(TextAlign.Center)
          PrimaryButton({ label: '重试', onTap: () => {
            const url = (router.getParams() as PdfParams | null)?.url ?? '';
            this.releaseDoc();
            this.open(url, '');
          } })
        }
        .width('100%')
        .padding(30)
      } else {
        Scroll() {
          Column({ space: 8 }) {
            if (this.pm !== null) {
              Image(this.pm)
                .width('100%')
                .objectFit(ImageFit.Contain)
                .borderRadius(8)
            }
            if (this.loading) {
              LoadingProgress().width(24).height(24).color(OgC.BRAND)
            }
            Row({ space: 16 }) {
              Text('‹ 上一页')
                .fontSize(14)
                .fontColor(this.cur > 0 ? OgC.BRAND : OgC.INK_SUB)
                .padding(10)
                .onClick(() => {
                  if (this.cur > 0 && !this.loading) {
                    this.render(this.cur - 1);
                  }
                })
              Blank()
              Text('下一页 ›')
                .fontSize(14)
                .fontColor(this.cur < this.pageCount - 1 ? OgC.BRAND : OgC.INK_SUB)
                .padding(10)
                .onClick(() => {
                  if (this.cur < this.pageCount - 1 && !this.loading) {
                    this.render(this.cur + 1);
                  }
                })
            }
            .width('100%')
            .padding({ left: 16, right: 16, bottom: 12 })
          }
          .width('100%')
        }
        .layoutWeight(1)
        .scrollBar(BarState.Off)
        .align(Alignment.Top)
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
  }
}
