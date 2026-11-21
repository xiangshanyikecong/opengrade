// 离线 JSON 缓存:应用 filesDir/offline 下按 key 落盘,弱网/断网时回退展示。
// 只缓存学生端只读数据(成绩列表/详情),服务端才是唯一真源。

import { fileIo as fs } from '@kit.CoreFileKit';
import { OgSession } from './OgSession';

export class OgCache {
  private static dir(): string {
    return OgSession.context().filesDir + '/offline';
  }

  // 保存(尽力而为,失败静默)。key 建议带主体维度,如 student_exams_<child>。
  static save(key: string, text: string): void {
    try {
      const dir = OgCache.dir();
      try {
        fs.mkdirSync(dir);
      } catch (e) {
        // 已存在
      }
      const f = fs.openSync(dir + '/' + key + '.json',
        fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
      try {
        fs.writeSync(f.fd, text);
      } finally {
        fs.closeSync(f.fd);
      }
    } catch (e) {
      // 缓存失败不影响主流程
    }
  }

  // 读取;不存在/损坏返回空串。
  static load(key: string): string {
    try {
      return fs.readTextSync(OgCache.dir() + '/' + key + '.json');
    } catch (e) {
      return '';
    }
  }

  static clear(): void {
    try {
      const dir = OgCache.dir();
      const names: string[] = fs.listFileSync(dir);
      for (const n of names) {
        try {
          fs.unlinkSync(dir + '/' + n);
        } catch (e) {
          // 忽略
        }
      }
    } catch (e) {
      // 目录不存在
    }
  }
}
