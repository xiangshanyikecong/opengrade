// 断点监听:折叠屏/平板宽屏自适应的统一数据源。
// 以媒体查询为权威(窗口逻辑宽度覆盖折叠形态切换/旋转/分屏),发布到 AppStorage:
//   @StorageProp('ogBp') bp: string = 'sm'  // sm <600vp ≤ md <840vp ≤ lg
//   sm=手机(含折叠态外屏);md=横屏/大折叠内屏;lg=平板/内屏宽态。
// EntryAbility.onWindowStageCreate 里 watch() 一次,全生命周期生效。
// 纯尺寸收口(内容列 maxWidth)不需要订阅——宽屏上限常量对窄屏天然无副作用;
// 只有布局随档位切换(双栏/列数)才需要 @StorageProp 订阅。

import { mediaquery } from '@kit.ArkUI';

export class OgBreakpoint {
  private static mdL: mediaquery.MediaQueryList | null = null;
  private static lgL: mediaquery.MediaQueryList | null = null;

  static watch(): void {
    if (OgBreakpoint.mdL !== null) {
      return;
    }
    try {
      OgBreakpoint.mdL = mediaquery.matchMediaSync('(min-width: 600vp)');
      OgBreakpoint.lgL = mediaquery.matchMediaSync('(min-width: 840vp)');
      const apply = (): void => {
        const lg: boolean = OgBreakpoint.lgL !== null && OgBreakpoint.lgL.matches;
        const md: boolean = OgBreakpoint.mdL !== null && OgBreakpoint.mdL.matches;
        AppStorage.setOrCreate('ogBp', lg ? 'lg' : (md ? 'md' : 'sm'));
      };
      OgBreakpoint.mdL.on('change', apply);
      OgBreakpoint.lgL.on('change', apply);
      apply();
    } catch (e) {
      AppStorage.setOrCreate('ogBp', 'sm');
    }
  }

  // 非响应式读取(弹层选项等在打开时取值即可的场景)。
  static now(): string {
    const v = AppStorage.get<string>('ogBp');
    return v !== undefined ? v : 'sm';
  }
}
