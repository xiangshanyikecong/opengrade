// 补传扫描:相册多选 / 系统相机拍照 → 拷入沙箱 → 分批接力上传(每批 5 张,失败卷单独报告)。
// 一期仅收 JPEG/PNG(OMR 服务端透视矫正对照片同样生效);PDF 拆页提示电脑端。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { camera, cameraPicker } from '@kit.CameraKit';
import { photoAccessHelper } from '@kit.MediaLibraryKit';
import { OgNet, OgError, UploadFile } from '../../common/OgNet';
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
    if (p !== null && p.exam !== undefined) {
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
                PrimaryButton({ label: '📷 拍照', onTap: () => this.pickCamera() })
                PrimaryButton({ label: '🖼 相册多选', onTap: () => this.pickAlbum() })
              }
              .width('100%')
              Text('整卷平放、光线均匀、四角完整;支持手机直拍(服务端自动透视矫正)。多页 PDF 请在电脑端导入。')
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
