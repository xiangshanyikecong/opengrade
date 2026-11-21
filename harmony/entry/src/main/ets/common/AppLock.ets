// 应用锁:本地 PIN(SHA-256 后存 preferences)+ 可选生物识别解锁(UserAuthKit)。
// 锁定时机:冷启动(Index 门禁)+ 退到后台再回前台(EntryAbility 锁定意图)。
// PIN/生物全部本地校验,不上传任何数据。

import { preferences } from '@kit.ArkData';
import { userAuth } from '@kit.UserAuthenticationKit';
import { cryptoFramework } from '@kit.CryptoArchitectureKit';
import { util } from '@kit.ArkTS';
import { OgSession } from './OgSession';

export class AppLock {
  private static prefs: preferences.Preferences | null = null;
  private static enabledValue: boolean = false;
  private static pinHash: string = '';
  private static bioValue: boolean = false;
  private static pendingLock: boolean = false; // 后台→前台锁定意图(单次消费)

  static async ensure(context: Context): Promise<void> {
    if (AppLock.prefs !== null) {
      return;
    }
    AppLock.prefs = await preferences.getPreferences(context, 'og_lock');
    AppLock.enabledValue = (await AppLock.prefs.get('on', false)) as boolean;
    AppLock.pinHash = (await AppLock.prefs.get('pin', '')) as string;
    AppLock.bioValue = (await AppLock.prefs.get('bio', false)) as boolean;
  }

  static enabled(): boolean {
    return AppLock.enabledValue && AppLock.pinHash !== '';
  }

  static biometricOn(): boolean {
    return AppLock.bioValue;
  }

  // 设备是否具备可用生物识别(指纹/人脸任一已录入)。
  static canBiometric(): boolean {
    try {
      userAuth.getAvailableStatus(userAuth.UserAuthType.FINGERPRINT, userAuth.AuthTrustLevel.ATL1);
      return true;
    } catch (e) {
      try {
        userAuth.getAvailableStatus(userAuth.UserAuthType.FACE, userAuth.AuthTrustLevel.ATL1);
        return true;
      } catch (e2) {
        return false;
      }
    }
  }

  static async setPin(pin: string): Promise<void> {
    AppLock.pinHash = await AppLock.sha256(pin);
    AppLock.enabledValue = true;
    await AppLock.persist();
  }

  static async clearPin(): Promise<void> {
    AppLock.enabledValue = false;
    AppLock.bioValue = false;
    await AppLock.persist();
  }

  static async setBiometric(on: boolean): Promise<void> {
    AppLock.bioValue = on;
    await AppLock.persist();
  }

  static async verify(pin: string): Promise<boolean> {
    if (AppLock.pinHash === '') {
      return false;
    }
    return (await AppLock.sha256(pin)) === AppLock.pinHash;
  }

  // 拉起系统生物识别弹窗(指纹/人脸);结果经回调返回。
  static biometricUnlock(onOk: () => void, onFail: (msg: string) => void): void {
    try {
      const authParam: userAuth.AuthParam = {
        challenge: new Uint8Array(16),
        authType: [userAuth.UserAuthType.FINGERPRINT, userAuth.UserAuthType.FACE],
        authTrustLevel: userAuth.AuthTrustLevel.ATL1
      };
      const widgetParam: userAuth.WidgetParam = {
        title: '解锁 OpenGrade'
      };
      const inst = userAuth.getUserAuthInstance(authParam, widgetParam);
      inst.on('result', {
        onResult: (r: userAuth.UserAuthResult) => {
          if (r.result === userAuth.UserAuthResultCode.SUCCESS) {
            onOk();
          } else {
            onFail('生物识别未通过,请用 PIN 解锁');
          }
        }
      });
      inst.start();
    } catch (e) {
      onFail('生物识别不可用,请用 PIN 解锁');
    }
  }

  // —— 生命周期:后台 → 前台 ——
  static onBackground(): void {
    if (AppLock.enabled()) {
      AppLock.pendingLock = true;
    }
  }

  // 消费锁定意图(多个入口竞争,先到先得)。
  static takeLockIntent(): boolean {
    const v = AppLock.pendingLock;
    AppLock.pendingLock = false;
    return v;
  }

  private static async persist(): Promise<void> {
    if (AppLock.prefs === null) {
      return;
    }
    await AppLock.prefs.put('on', AppLock.enabledValue);
    await AppLock.prefs.put('pin', AppLock.pinHash);
    await AppLock.prefs.put('bio', AppLock.bioValue);
    await AppLock.prefs.flush();
  }

  private static async sha256(text: string): Promise<string> {
    const enc = new util.TextEncoder();
    const data = enc.encodeInto(text);
    const md = cryptoFramework.createMd('SHA-256');
    await md.update({ data: data } as cryptoFramework.DataBlob);
    const digest = await md.digest();
    const out: string[] = [];
    for (const b of digest.data) {
      out.push((b < 16 ? '0' : '') + b.toString(16));
    }
    return out.join('');
  }
}
