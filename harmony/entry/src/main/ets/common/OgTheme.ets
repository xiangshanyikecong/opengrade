// 主题常量:与 web/common.css 的 CSS 变量同源(--brand 青绿系)。
// 直接用字符串常量而非 $r 引用,便于 Canvas 取色与暗色后续扩展。

export class OgC {
  static readonly BRAND: string = '#00B389';
  static readonly BRAND_DEEP: string = '#00996F';
  static readonly BRAND_LIGHT: string = '#E8F8F4';
  static readonly INK: string = '#1F2937';
  static readonly INK_SUB: string = '#66707D';
  static readonly LINE: string = '#E5E7EB';
  static readonly BG: string = '#F5F7FA';
  static readonly CARD: string = '#FFFFFF';
  static readonly BAD: string = '#E53935';
  static readonly GOOD: string = '#00A152';
  static readonly WARN: string = '#F59E0B';
  static readonly WARN_BG: string = '#FDF6EC';
  static readonly BAD_BG: string = '#FDECE8';
  static readonly FLAG: string = '#D97706';
  static readonly BLUE: string = '#2563EB';
  static readonly STAGE_BLUE: string = '#EFF6FF';
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
