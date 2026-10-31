// 补传扫描:相册多选 / 系统相机拍照 / 本地 PDF 拆页(pdfService 渲染逐页出 JPEG)
// → 拷入沙箱 → 分批接力上传(每批 5 张,失败卷单独报告)。
// OMR 服务端透视矫正对手机直拍同样生效。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { camera, cameraPicker } from '@kit.CameraKit';
import { photoAccessHelper } from '@kit.MediaLibraryKit';
import { picker } from '@kit.CoreFileKit';
import { fileIo as fs } from '@kit.CoreFileKit';
import { image } from '@kit.ImageKit';
import { pdfService } from '@kit.PDFKit';
import { OgNet, OgError, UploadFile } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
import { OgC, OgDim } from '../../common/OgTheme';
import { ErrorView, PrimaryButton, SectionHeader, TitleBar } from '../../components/Ui';

interface UploadParams {
  exam: string;
}

interface ScanBatchResp {
  added: number;
  students: number;
  failed: FailedItem[];
  notes: string[];
  merged: string[];
}

interface FailedItem {
  index: number;
  error: string;
}

const BATCH: number = 5;

@Entry
@Component
struct UploadScansPage {
  @State examId: string = '';
  @State picked: string[] = []; // 沙箱路径
  @State uploaded: number = 0;
  @State busy: boolean = false;
  @State logLines: string[] = [];
  @State failed: boolean = false;
  @State failMsg: string = '';

  aboutToAppear(): void {
    const p = router.getParams() as UploadParams | null;
    if (p !== null && p !== undefined && p.exam !== undefined) {
      this.examId = p.exam;
    } else {
      this.failed = true;
      this.failMsg = '缺少考试参数';
    }
  }

  private contentTypeOf(path: string): string {
    if (path.endsWith('.png') || path.endsWith('.PNG')) {
      return 'image/png';
    }
    return 'image/jpeg';
  }

  private async pickAlbum(): Promise<void> {
    const picker = new photoAccessHelper.PhotoViewPicker();
    const opts: photoAccessHelper.PhotoSelectOptions = {
      MIMEType: photoAccessHelper.PhotoViewMIMETypes.IMAGE_TYPE,
      maxSelectNumber: 30
    };
    try {
      const result: photoAccessHelper.PhotoSelectResult = await picker.select(opts);
      await this.addUris(result.photoUris);
    } catch (e) {
      // 用户取消
    }
  }

  private async pickCamera(): Promise<void> {
    try {
      const profile: cameraPicker.PickerProfile = {
        cameraPosition: camera.CameraPosition.CAMERA_POSITION_BACK
      };
      const result: cameraPicker.PickerResult =
        await cameraPicker.pick(getContext(this) as Context, [cameraPicker.PickerMediaType.PHOTO], profile);
      if (result.resultUri !== '') {
        await this.addUris([result.resultUri]);
      }
    } catch (e) {
      // 用户取消
    }
  }

  // PDF 拆页:选 PDF → 拷沙箱 → pdfService 逐页渲染(长边约 2200px ≈265dpi,保二维码可读)
  // → 编码 JPEG 入待上传队列。服务端多页按页考号归组,卡纸跳页只告警对应学生。
  private async importPdf(): Promise<void> {
    const dp = new picker.DocumentViewPicker();
    const opts: picker.DocumentSelectOptions = { maxSelectNumber: 1 };
    let uri = '';
    try {
      const uris = await dp.select(opts);
      if (uris.length === 0) {
        return;
      }
      uri = uris[0];
    } catch (e) {
      return; // 用户取消
    }
    this.busy = true;
    this.logLines.unshift('ℹ 正在拆分 PDF…');
    let pdfPath = '';
    try {
      pdfPath = await OgNet.copyUriToCache(uri, 'pdf_' + Date.now() + '.pdf');
      const doc = new pdfService.PdfDocument();
      const loadResult = doc.loadDocument(pdfPath, '');
      if (loadResult !== pdfService.ParseResult.PARSE_SUCCESS) {
        throw new OgError(0, '无法解析该 PDF(可能已加密或损坏)');
      }
      const n = doc.getPageCount();
      if (n <= 0) {
        throw new OgError(0, 'PDF 没有页面');
      }
      const packer = image.createImagePacker();
      for (let i = 0; i < n; i++) {
        const page = doc.getPage(i);
        let pm: image.PixelMap | null = null;
        try {
          const w = page.getWidth();
          const h = page.getHeight();
          const matrix: pdfService.PdfMatrix = { x: 0, y: 0, width: w, height: h, rotate: 0 };
          const target = 2200;
          const bw = w >= h ? target : Math.max(400, Math.round(target * w / h));
          const bh = w >= h ? Math.max(400, Math.round(target * h / w)) : target;
          pm = page.getAreaPixelMap(matrix, bw, bh, false, false);
          const packing: image.PackingOption = { format: 'image/jpeg', quality: 88 };
          const jpg = await packer.packToData(pm, packing);
          const name = 'pdfpg_' + Date.now() + '_' + this.picked.length + '.jpg';
          const outPath = OgSession.context().cacheDir + '/' + name;
          const f = fs.openSync(outPath, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
          try {
            fs.writeSync(f.fd, jpg);
          } finally {
            fs.closeSync(f.fd);
          }
          this.picked.push(outPath);
          this.logLines.unshift('✓ 第 ' + (i + 1) + '/' + n + ' 页已拆出');
        } catch (e) {
          this.logLines.unshift('✗ 第 ' + (i + 1) + ' 页渲染失败,已跳过');
        } finally {
          if (pm !== null) {
            try {
              pm.release();
            } catch (e) {
              // 忽略
            }
          }
          try {
            page.release();
          } catch (e) {
            // 忽略
          }
        }
      }
      doc.releaseDocument();
      this.logLines.unshift('✓ 拆页完成,共待上传 ' + this.picked.length + ' 张');
    } catch (e) {
      this.logLines.unshift('✗ PDF 导入失败:' + (e instanceof OgError ? e.message : '文件不可读'));
    } finally {
      if (pdfPath !== '') {
        try {
          fs.unlinkSync(pdfPath);
        } catch (e) {
          // 忽略
        }
      }
      this.busy = false;
    }
  }

  private async addUris(uris: string[]): Promise<void> {
    for (let i = 0; i < uris.length; i++) {
      const name = 'scan_' + Date.now() + '_' + this.picked.length + '.jpg';
      try {
        const path = await OgNet.copyUriToCache(uris[i], name);
        this.picked.push(path);
      } catch (e) {
        this.logLines.unshift('✗ 一张图片读取失败,已跳过');
      }
    }
  }

  private async doUpload(): Promise<void> {
    if (this.picked.length === 0) {
      promptAction.showToast({ message: '请先选择或拍摄扫描件' });
      return;
    }
    this.busy = true;
    this.uploaded = 0;
    let totalAdded = 0;
    for (let start = 0; start < this.picked.length; start += BATCH) {
      const files: UploadFile[] = [];
      const end = Math.min(start + BATCH, this.picked.length);
      for (let i = start; i < end; i++) {
        files.push({
          field: 'images',
          fileName: 'scan_' + (i + 1) + '.jpg',
          contentType: this.contentTypeOf(this.picked[i]),
          filePath: this.picked[i]
        } as UploadFile);
      }
      try {
        const resp = await OgNet.upload('/api/v1/exams/' + this.examId + '/scans', {}, files);
        const m = JSON.parse(resp) as ScanBatchResp;
        totalAdded += m.added ?? 0;
        this.uploaded = end;
        if (m.failed !== undefined && m.failed.length > 0) {
          for (const f of m.failed) {
            this.logLines.unshift('✗ 批内第 ' + f.index + ' 张:' + f.error);
          }
        }
        if (m.notes !== undefined) {
          for (const n of m.notes) {
            this.logLines.unshift('ℹ ' + n);
          }
        }
        if (m.merged !== undefined && m.merged.length > 0) {
          this.logLines.unshift('ℹ 跨批合并:' + m.merged.join('、'));
        }
      } catch (e) {
        this.logLines.unshift('✗ 第 ' + (start + 1) + '~' + end + ' 张上传失败:' +
          (e instanceof OgError ? e.message : '网络异常') + '(已入库的批次不受影响,可重试续传)');
        break; // 断点续传:失败即停,已入库批次保留
      }
    }
    if (totalAdded > 0) {
      this.logLines.unshift('✓ 本次共入库 ' + totalAdded + ' 卷');
      this.picked = [];
      this.uploaded = 0;
    }
    this.busy = false;
  }

  build() {
    Column() {
      TitleBar({
        title: '补传扫描',
        subtitle: this.examId,
        onBack: () => router.back()
      })
      Scroll() {
        Column({ space: OgDim.GAP }) {
          if (this.failed) {
            ErrorView({ msg: this.failMsg })
          } else {
            Column({ space: 8 }) {
              SectionHeader({ text: '1. 选择或拍摄答题卡' })
              Row({ space: 10 }) {
                PrimaryButton({ label: '📷 拍照', fill: true, onTap: () => this.pickCamera() })
                PrimaryButton({ label: '🖼 相册多选', fill: true, onTap: () => this.pickAlbum() })
              }
              .width('100%')
              Row({ space: 10 }) {
                PrimaryButton({
                  label: '📄 从 PDF 导入(自动拆页)',
                  full: true,
                  usable: !this.busy,
                  onTap: () => this.importPdf()
                })
              }
              .width('100%')
              Text('整卷平放、光线均匀、四角完整;支持手机直拍(服务端自动透视矫正)。扫描仪出的 PDF 文件选「从 PDF 导入」自动逐页拆分(加密 PDF 不支持)。')
                .fontSize(11).fontColor(OgC.INK_SUB)
            }
            .width('100%')
            .padding(14)
            .backgroundColor(OgC.CARD)
            .borderRadius(OgDim.RADIUS)
            .alignItems(HorizontalAlign.Start)

            Column({ space: 8 }) {
              SectionHeader({ text: '2. 上传', extra: this.picked.length > 0 ? '已选 ' + this.picked.length + ' 张' : '' })
              if (this.picked.length > 0) {
                Progress({ value: this.uploaded, total: this.picked.length, type: ProgressType.Linear })
                  .color(OgC.BRAND).width('100%').height(8)
                Text(this.busy ? '上传中 ' + this.uploaded + '/' + this.picked.length : '待上传 ' + this.picked.length + ' 张')
                  .fontSize(12).fontColor(OgC.INK_SUB)
              }
              PrimaryButton({
                full: true,
                label: this.busy ? '上传中…' : (this.picked.length > 0 ? '开始上传(' + this.picked.length + ' 张)' : '开始上传'),
                usable: !this.busy && this.picked.length > 0,
                onTap: () => this.doUpload()
              })
            }
            .width('100%')
            .padding(14)
            .backgroundColor(OgC.CARD)
            .borderRadius(OgDim.RADIUS)
            .alignItems(HorizontalAlign.Start)

            if (this.logLines.length > 0) {
              Column({ space: 6 }) {
                SectionHeader({ text: '上传反馈' })
                ForEach(this.logLines, (line: string) => {
                  Text(line).fontSize(12).fontColor(line.startsWith('✓') ? OgC.GOOD :
                    (line.startsWith('✗') ? OgC.BAD : OgC.INK_SUB))
                }, (line: string, idx: number) => idx + '-' + line)
              }
              .width('100%')
              .padding(14)
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)
              .alignItems(HorizontalAlign.Start)
            }
            Text('').height(20)
          }
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
  }
}
