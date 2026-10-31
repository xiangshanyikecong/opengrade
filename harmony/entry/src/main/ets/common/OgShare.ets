// 系统分享:把沙箱内文件(成绩单 PDF/成绩册等)交给系统分享面板。
// 文件须在应用沙箱内(fileUri 转换);接收方权限由系统分享中心临时授予。

import { systemShare } from '@kit.ShareKit';
import { uniformTypeDescriptor } from '@kit.ArkData';
import { fileUri } from '@kit.CoreFileKit';
import { fileIo as fs } from '@kit.CoreFileKit';
import { OgNet } from './OgNet';
import { OgSession } from './OgSession';

export class OgShare {
  // 分享沙箱文件(utd 用通用文件类型,兼容性最稳)。
  static async shareFile(sandboxPath: string, title: string): Promise<void> {
    const uri = fileUri.getUriFromPath(sandboxPath);
    const data: systemShare.SharedData = new systemShare.SharedData({
      utd: uniformTypeDescriptor.UniformDataType.FILE,
      content: uri
    });
    data.title = title;
    const controller = systemShare.getSystemShareController(data);
    await controller.startSystemShare();
  }

  // 拉取远端接口内容 → 落沙箱 → 分享(成绩单 PDF 等)。
  static async shareUrl(path: string, fileName: string, title: string): Promise<void> {
    const buf = await OgNet.getBytes(path, 600000);
    const dir = OgSession.context().cacheDir + '/share';
    try {
      fs.mkdirSync(dir);
    } catch (e) {
      // 已存在
    }
    const sandboxPath = dir + '/' + fileName;
    const f = fs.openSync(sandboxPath, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
    try {
      fs.writeSync(f.fd, buf);
    } finally {
      fs.closeSync(f.fd);
    }
    await OgShare.shareFile(sandboxPath, title);
  }

  // 已在沙箱的二进制直接分享。
  static async shareBuffer(buf: ArrayBuffer, sandboxPath: string, title: string): Promise<void> {
    const f = fs.openSync(sandboxPath, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
    try {
      fs.writeSync(f.fd, buf);
    } finally {
      fs.closeSync(f.fd);
    }
    await OgShare.shareFile(sandboxPath, title);
  }
}
