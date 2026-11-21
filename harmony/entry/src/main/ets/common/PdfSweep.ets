// 登出/应用锁流程的成绩单 PDF 缓存清扫:pdfview_*.pdf 留在沙箱缓存会随时间累积,
// 且含学生成绩单,登出后不应可读(评审 PdfViewerPage.ets:76/CWE-459)。
import fs from '@ohos.file.fs';
import { OgSession } from './OgSession';

export class PdfSweep {
  static sweep(): void {
    try {
      const dir = OgSession.context().cacheDir;
      const names: string[] = fs.listFileSync(dir);
      for (const n of names) {
        if (n.startsWith('pdfview_')) {
          try {
            fs.unlinkSync(dir + '/' + n);
          } catch (e) {
            // 忽略单个文件失败
          }
        }
      }
    } catch (e) {
      // 目录不可读时忽略:缓存随系统回收
    }
  }
}
