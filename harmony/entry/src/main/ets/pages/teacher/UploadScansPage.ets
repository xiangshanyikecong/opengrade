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
    let doc: pdfService.PdfDocument | null = null;
    try {
      pdfPath = await OgNet.copyUriToCache(uri, 'pdf_' + Date.now() + '.pdf');
      doc = new pdfService.PdfDocument();
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
          const name = 'pdfpg_' + Date.now() + '_' + this.picked.length + '.jpg';
          const outPath = OgSession.context().cacheDir + '/' + name;
          const f = fs.openSync(outPath, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
          try {
            // packToData 是 API 13 才有的接口(项目 compatibleSdkVersion 12):
            // packToFile(fd) API 12 即可用,直接编码落盘(评审 UploadScansPage.ets:136)
            await packer.packToFile(f.fd, pm, packing);
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
      this.logLines.unshift('✓ 拆页完成,共待上传 ' + this.picked.length + ' 张');
    } catch (e) {
      this.logLines.unshift('✗ PDF 导入失败:' + (e instanceof OgError ? e.message : '文件不可读'));
    } finally {
      // 文档释放入 finally:getPage 等中途抛异常也不再泄漏(评审 UploadScansPage.ets:164)
      if (doc !== null) {
        try {
          doc.releaseDocument();
        } catch (e) {
          // 忽略
        }
      }
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
    if (this.busy) {
      promptAction.showToast({ message: '上传进行中,新选的图片请等本轮结束后再选' });
      return; // 选择与上传不重叠,批次读取固定队列(评审 UploadScansPage.ets:200)
    }
    for (let i = 0; i < uris.length; i++) {
      // 保留原始扩展名:copyUriToCache 只复制字节,统一 .jpg 会把 PNG 声明成 image/jpeg(评审 UploadScansPage.ets:182)
      const dot = uris[i].lastIndexOf('.');
      const ext = dot >= 0 ? uris[i].slice(dot).toLowerCase() : '.jpg';
      const name = 'scan_' + Date.now() + '_' + i + (ext === '.jpeg' ? '.jpg' : ext);
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
    // 上传开始时固定待发送队列快照:相册复制仍在继续也不会混进本批(评审 UploadScansPage.ets:200)
    const queue: string[] = [...this.picked];
    this.busy = true;
    this.uploaded = 0;
    let totalAdded = 0;
    let sent = 0; // 已确认入库的前缀张数
    for (let start = 0; start < queue.length; start += BATCH) {
      const files: UploadFile[] = [];
      const end = Math.min(start + BATCH, queue.length);
      for (let i = start; i < end; i++) {
        const dot = queue[i].lastIndexOf('.');
        const ext = dot >= 0 ? queue[i].slice(dot) : '.jpg';
        files.push({
          field: 'images',
          fileName: 'scan_' + (i + 1) + ext,
          contentType: this.contentTypeOf(queue[i]),
          filePath: queue[i]
        } as UploadFile);
      }
      try {
        const resp = await OgNet.upload('/api/v1/exams/' + this.examId + '/scans', {}, files);
        const m = JSON.parse(resp) as ScanBatchResp;
        const added = m.added ?? 0;
        totalAdded += added;
        sent = start + added; // 批内按服务端确认数量推进前缀(服务器按序入库)
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
          (e instanceof OgError ? e.message : '网络异常') + '(失败与未发送项已保留,可重试续传)');
        break; // 断点续传:失败即停
      }
    }
    // 只清掉已确认入库的前缀(并删除其沙箱副本):失败/未发送项保留可续传
    //(评审 UploadScansPage.ets:235/237)
    for (let i = 0; i < sent && i < queue.length; i++) {
      try {
        fs.unlinkSync(queue[i]);
      } catch (e) {
        // 忽略
      }
    }
    this.picked = queue.slice(sent);
    this.uploaded = 0;
    if (totalAdded > 0) {
      this.logLines.unshift('✓ 本次共入库 ' + totalAdded + ' 卷' +
        (this.picked.length > 0 ? '(剩 ' + this.picked.length + ' 张待续传)' : ''));
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
        .constraintSize({ maxWidth: OgDim.MAXW })
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
