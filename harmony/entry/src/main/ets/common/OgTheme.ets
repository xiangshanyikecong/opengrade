// 主题常量:与 web/common.css 的 CSS 变量同源(--brand 青绿系)。
// 直接用字符串常量而非 $r 引用,便于 Canvas 取色。
// 深色模式:颜色经静态 getter 派发,OgC.setMode/setSystem 切换后,
// 新渲染的页面/组件即用新配色(已驻留页面在下一次进入时刷新)。

interface ThemePalette {
  brand: string;
  brandDeep: string;
  brandLight: string;
  ink: string;
  inkSub: string;
  line: string;
  bg: string;
  card: string;
  bad: string;
  good: string;
  warn: string;
  warnBg: string;
  badBg: string;
  flag: string;
  blue: string;
  stageBlue: string;
}

const LIGHT: ThemePalette = {
  brand: '#00B389',
  brandDeep: '#00996F',
  brandLight: '#E8F8F4',
  ink: '#1F2937',
  inkSub: '#66707D',
  line: '#E5E7EB',
  bg: '#F5F7FA',
  card: '#FFFFFF',
  bad: '#E53935',
  good: '#00A152',
  warn: '#F59E0B',
  warnBg: '#FDF6EC',
  badBg: '#FDECE8',
  flag: '#D97706',
  blue: '#2563EB',
  stageBlue: '#EFF6FF'
};

const DARK: ThemePalette = {
  brand: '#00C99A',
  brandDeep: '#00B389',
  brandLight: '#0E3B31',
  ink: '#E6EAF0',
  inkSub: '#9AA4B2',
  line: '#2C3440',
  bg: '#12161C',
  card: '#1B212B',
  bad: '#FF6B63',
  good: '#2ECC71',
  warn: '#FBBF24',
  warnBg: '#3A2F14',
  badBg: '#3A1D1A',
  flag: '#F59E0B',
  blue: '#60A5FA',
  stageBlue: '#16233B'
};

export class OgC {
  private static mode: string = 'auto'; // auto | light | dark(Me 页可改,存 OgSession)
  private static sysDark: boolean = false; // 跟随系统色(EntryAbility 维护)

  static get dark(): boolean {
    return OgC.mode === 'dark' || (OgC.mode === 'auto' && OgC.sysDark);
  }

  private static get p(): ThemePalette {
    return OgC.dark ? DARK : LIGHT;
  }

  // 手动模式切换('auto'|'light'|'dark')。
  static setMode(m: string): void {
    OgC.mode = m;
  }

  // 系统深浅色变化(冷启动/配置变化时由 EntryAbility 调)。
  static setSystemDark(d: boolean): void {
    OgC.sysDark = d;
  }

  static get BRAND(): string {
    return OgC.p.brand;
  }

  static get BRAND_DEEP(): string {
    return OgC.p.brandDeep;
  }

  static get BRAND_LIGHT(): string {
    return OgC.p.brandLight;
  }

  static get INK(): string {
    return OgC.p.ink;
  }

  static get INK_SUB(): string {
    return OgC.p.inkSub;
  }

  static get LINE(): string {
    return OgC.p.line;
  }

  static get BG(): string {
    return OgC.p.bg;
  }

  static get CARD(): string {
    return OgC.p.card;
  }

  static get BAD(): string {
    return OgC.p.bad;
  }

  static get GOOD(): string {
    return OgC.p.good;
  }

  static get WARN(): string {
    return OgC.p.warn;
  }

  static get WARN_BG(): string {
    return OgC.p.warnBg;
  }

  static get BAD_BG(): string {
    return OgC.p.badBg;
  }

  static get FLAG(): string {
    return OgC.p.flag;
  }

  static get BLUE(): string {
    return OgC.p.blue;
  }

  static get STAGE_BLUE(): string {
    return OgC.p.stageBlue;
  }
}

export class OgDim {
  static readonly PAD: number = 16;
  static readonly GAP: number = 10;
  static readonly RADIUS: number = 12;
  static readonly TITLE: number = 20;
  static readonly BODY: number = 15;
  static readonly SUB: number = 13;
  static readonly SMALL: number = 12;
}
