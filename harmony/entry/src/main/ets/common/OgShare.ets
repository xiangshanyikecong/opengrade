// 跨应用打开/转发:把沙箱文件经 fileUri 交给系统选择其他应用打开
// (PDF 阅读器/网盘/聊天应用,后续转发即分享)。只用 Want + fileUri 基础 API,零 Kit 依赖。

import { common, Want } from '@kit.AbilityKit';
import { fileUri } from '@kit.CoreFileKit';
import { OgSession } from './OgSession';

export class OgShare {
  // FLAG_GRANT_READ_URI_PERMISSION:临时授予目标应用读该 URI 的权限
  private static readonly FLAG_GRANT_READ: number = 1;

  // 调起「选择应用打开」:失败(无应用可处理)由调用方 catch 提示。
  static async openWith(sandboxPath: string, mimeType: string): Promise<void> {
    const ctx = OgSession.context() as common.UIAbilityContext;
    const want: Want = {
      action: 'ohos.want.action.viewData',
      uri: fileUri.getUriFromPath(sandboxPath),
      type: mimeType,
      flags: OgShare.FLAG_GRANT_READ
    };
    await ctx.startAbility(want);
  }
}
