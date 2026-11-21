// 会话管理:baseUrl/token/me 的 preferences 持久化(应用沙箱内)。
// token 由服务端 native 登录下发,以 Bearer 头携带;logout/改密时服务端吊销。

import { preferences } from '@kit.ArkData';
import { Me } from '../model/Api';

const STORE_NAME: string = 'og_session';
const KEY_BASE: string = 'baseUrl';
const KEY_TOKEN: string = 'token';
const KEY_ME: string = 'me';
const KEY_LOW_DATA: string = 'lowData';
const KEY_THEME: string = 'themeMode';

export class OgSession {
  private static prefs: preferences.Preferences | null = null;
  private static baseUrlValue: string = '';
  private static tokenValue: string = '';
  private static meValue: Me | null = null;
  private static ctx: Context | null = null;
  private static lowDataValue: boolean = false;
  private static themeModeValue: string = 'auto'; // auto | light | dark

  // 由首页/登录页在 aboutToAppear 时初始化(context 取 UIAbility 上下文)。
  static async init(context: Context): Promise<void> {
    OgSession.ctx = context;
    if (OgSession.prefs !== null) {
      return;
    }
    OgSession.prefs = await preferences.getPreferences(context, STORE_NAME);
    OgSession.baseUrlValue = (await OgSession.prefs.get(KEY_BASE, '')) as string;
    OgSession.tokenValue = (await OgSession.prefs.get(KEY_TOKEN, '')) as string;
    const meStr = (await OgSession.prefs.get(KEY_ME, '')) as string;
    if (meStr !== '') {
      try {
        OgSession.meValue = JSON.parse(meStr) as Me;
      } catch (e) {
        OgSession.meValue = null;
      }
    }
    OgSession.lowDataValue = (await OgSession.prefs.get(KEY_LOW_DATA, false)) as boolean;
    OgSession.themeModeValue = (await OgSession.prefs.get(KEY_THEME, 'auto')) as string;
  }

  // 深色模式策略:auto=跟随系统 | light | dark(颜色经 OgC getter 派发)。
  static themeMode(): string {
    return OgSession.themeModeValue;
  }

  static async setThemeMode(v: string): Promise<void> {
    OgSession.themeModeValue = v;
    if (OgSession.prefs !== null) {
      OgSession.prefs.putSync(KEY_THEME, v);
      // 评审 OgSession.ets:53 — 等待设置写盘完成,失败时抛给调用方提示重试
      await OgSession.prefs.flush();
    }
  }

  // 通用 JSON 值存取(本地已读标记/提醒 id 列表等;与登录态同库,按设备隔离)。
  static async getJson(key: string): Promise<string> {
    if (OgSession.prefs === null) {
      return '';
    }
    return (await OgSession.prefs.get(key, '')) as string;
  }

  static async setJson(key: string, value: string): Promise<void> {
    if (OgSession.prefs === null) {
      return;
    }
    await OgSession.prefs.put(key, value);
    await OgSession.prefs.flush();
  }

  // 省流模式:图片解码上限降到 1200px 且关闭阅卷预加载(弱网/流量敏感场景)。
  static lowData(): boolean {
    return OgSession.lowDataValue;
  }

  static async setLowData(v: boolean): Promise<void> {
    OgSession.lowDataValue = v;
    if (OgSession.prefs !== null) {
      OgSession.prefs.putSync(KEY_LOW_DATA, v);
      // 评审 OgSession.ets:82 — 等待设置写盘完成,失败时抛给调用方提示重试
      await OgSession.prefs.flush();
    }
  }

  // 图片解码尺寸上限:省流时压到 1200,否则用调用方建议值。
  static imageCap(preferred: number): number {
    if (OgSession.lowDataValue) {
      return Math.min(1200, preferred);
    }
    return preferred;
  }

  static baseUrl(): string {
    return OgSession.baseUrlValue;
  }

  static context(): Context {
    return OgSession.ctx as Context;
  }

  static token(): string {
    return OgSession.tokenValue;
  }

  static me(): Me | null {
    return OgSession.meValue;
  }

  static setBaseUrl(url: string): void {
    OgSession.baseUrlValue = url.replace(/\/+$/, '');
    if (OgSession.prefs !== null) {
      OgSession.prefs.putSync(KEY_BASE, OgSession.baseUrlValue);
      OgSession.prefs.flush();
    }
  }

  static setLogin(url: string, token: string, me: Me): void {
    OgSession.baseUrlValue = url.replace(/\/+$/, '');
    OgSession.tokenValue = token;
    OgSession.meValue = me;
    if (OgSession.prefs !== null) {
      OgSession.prefs.putSync(KEY_BASE, OgSession.baseUrlValue);
      OgSession.prefs.putSync(KEY_TOKEN, token);
      OgSession.prefs.putSync(KEY_ME, JSON.stringify(me));
      OgSession.prefs.flush();
    }
  }

  static setMe(me: Me): void {
    OgSession.meValue = me;
    if (OgSession.prefs !== null) {
      OgSession.prefs.putSync(KEY_ME, JSON.stringify(me));
      OgSession.prefs.flush();
    }
  }

  static clearToken(): void {
    OgSession.tokenValue = '';
    OgSession.meValue = null;
    if (OgSession.prefs !== null) {
      OgSession.prefs.deleteSync(KEY_TOKEN);
      OgSession.prefs.deleteSync(KEY_ME);
      OgSession.prefs.flush();
    }
  }

  static loggedIn(): boolean {
    return OgSession.tokenValue !== '' && OgSession.meValue !== null;
  }

  static isStaff(): boolean {
    const m = OgSession.meValue;
    return m !== null && m.role !== 'student' && m.role !== 'parent';
  }

  static isAdmin(): boolean {
    const m = OgSession.meValue;
    return m !== null && m.role === 'admin';
  }

  static isManager(): boolean {
    const m = OgSession.meValue;
    return m !== null && (m.role === 'admin' || m.role === 'leader');
  }

  static isStudentish(): boolean {
    const m = OgSession.meValue;
    return m !== null && (m.role === 'student' || m.role === 'parent');
  }

  // 多服务器配置:家里/校内地址切换(列表存 JSON)。
  static async servers(): Promise<string[]> {
    if (OgSession.prefs === null) {
      return [];
    }
    const raw = (await OgSession.prefs.get('servers', '')) as string;
    if (raw === '') {
      return [];
    }
    try {
      return JSON.parse(raw) as string[];
    } catch (e) {
      return [];
    }
  }

  // 评审 OgSession.ets:192 — 服务器列表读-合并-写串行化,防止并发 rememberServer 互相覆盖丢地址
  private static serverWriteQ: Promise<void> = Promise.resolve();

  static async rememberServer(url: string): Promise<void> {
    if (OgSession.prefs === null) {
      return;
    }
    const p = OgSession.prefs;
    OgSession.serverWriteQ = OgSession.serverWriteQ
      .then(() => OgSession.rememberServerLocked(p, url), () => undefined);
    await OgSession.serverWriteQ;
  }

  private static async rememberServerLocked(p: preferences.Preferences, url: string): Promise<void> {
    const list = await OgSession.servers();
    const norm = url.replace(/\/+$/, '');
    const out: string[] = [norm];
    for (const s of list) {
      if (s !== norm && out.length < 6) {
        out.push(s);
      }
    }
    await p.put('servers', JSON.stringify(out));
    await p.flush();
  }
}
