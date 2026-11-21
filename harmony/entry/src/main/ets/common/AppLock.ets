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
    // 先读进局部变量、全部成功后才落类字段:读取中途失败时 prefs 仍为 null,
    // 下次调用可重试;并发调用者也不会拿到半初始化状态(评审 AppLock.ets:18)
    const prefs = await preferences.getPreferences(context, 'og_lock');
    const on = (await prefs.get('on', false)) as boolean;
    const pin = (await prefs.get('pin', '')) as string;
    const bio = (await prefs.get('bio', false)) as boolean;
    AppLock.prefs = prefs;
    AppLock.enabledValue = on;
    AppLock.pinHash = pin;
    AppLock.bioValue = bio;
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

  private static failCount: number = 0;
  private static lockUntilMs: number = 0;
  private static failsLoaded: boolean = false;

  static async verify(pin: string): Promise<boolean> {
    if (AppLock.pinHash === '') {
      return false;
    }
    // 失败计数持久化:重启不清零,4~6 位 PIN 不能被无限穷举(评审 LockPage.ets:49/CWE-307)
    if (!AppLock.failsLoaded && AppLock.prefs !== null) {
      try {
        AppLock.failCount = (await AppLock.prefs.get('fails', 0)) as number;
        AppLock.lockUntilMs = (await AppLock.prefs.get('lockUntil', 0)) as number;
      } catch (e) {
        // 读取失败按 0 处理
      }
      AppLock.failsLoaded = true;
    }
    const ok = (await AppLock.sha256(pin)) === AppLock.pinHash;
    if (ok) {
      AppLock.resetFails();
    }
    return ok;
  }

  // 连错后的临时锁定剩余毫秒(0 = 未锁定)。
  static lockedRemainMs(): number {
    return Math.max(0, AppLock.lockUntilMs - Date.now());
  }

  // 一次 PIN 校验失败:计数 +1,连错 5 次起指数等待(30s 起,封顶 15 分钟)。
  static recordFail(): void {
    AppLock.failCount++;
    if (AppLock.failCount >= 5) {
      const step = AppLock.failCount - 5;
      AppLock.lockUntilMs = Date.now() + Math.min(15 * 60 * 1000, 30 * 1000 * Math.pow(2, step));
    }
    if (AppLock.prefs !== null) {
      AppLock.prefs.put('fails', AppLock.failCount).catch(() => {});
      AppLock.prefs.put('lockUntil', AppLock.lockUntilMs).catch(() => {});
      AppLock.prefs.flush().catch(() => {});
    }
  }

  private static resetFails(): void {
    AppLock.failCount = 0;
    AppLock.lockUntilMs = 0;
    if (AppLock.prefs !== null) {
      AppLock.prefs.put('fails', 0).catch(() => {});
      AppLock.prefs.put('lockUntil', 0).catch(() => {});
      AppLock.prefs.flush().catch(() => {});
    }
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

  // 恢复锁定意图:锁屏页入栈失败时不吞意图,下次前台仍会锁(评审 EntryAbility.ets:39)。
  static restoreLockIntent(): void {
    AppLock.pendingLock = true;
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
