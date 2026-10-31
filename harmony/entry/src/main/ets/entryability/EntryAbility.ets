// 应用入口:加载 Index 分发页(未登录→登录页;学生→查分;教职员→工作台)。
// 深色模式:跟随系统(Configuration 回调)+ Me 页手动档位,经 OgC getter 全局生效。
// 应用锁:退后台记锁定意图,回前台延时拉起锁屏页(冷启动由 Index 门禁负责)。

import { AbilityConstant, UIAbility, Want, ConfigurationConstant, Configuration } from '@kit.AbilityKit';
import { window, router } from '@kit.ArkUI';
import { OgC } from '../common/OgTheme';
import { AppLock } from '../common/AppLock';

export default class EntryAbility extends UIAbility {
  onCreate(want: Want, launchParam: AbilityConstant.LaunchParam): void {
    OgC.setSystemDark(this.systemDark(this.context.config));
  }

  onWindowStageCreate(windowStage: window.WindowStage): void {
    windowStage.loadContent('pages/Index', (err) => {
      if (err.code) {
        console.error('loadContent failed: ' + JSON.stringify(err));
      }
    });
  }

  onWindowStageDestroy(): void {
  }

  onConfigurationUpdate(newConfig: Configuration): void {
    OgC.setSystemDark(this.systemDark(newConfig));
  }

  private systemDark(config: Configuration | undefined): boolean {
    return config !== undefined && config.colorMode === ConfigurationConstant.ColorMode.COLOR_MODE_DARK;
  }

  onForeground(): void {
    // 淡入完成后拉锁屏,避免与窗口恢复竞态;锁定意图单次消费,与 Index 门禁互斥
    setTimeout(() => {
      if (AppLock.takeLockIntent()) {
        router.pushUrl({ url: 'pages/common/LockPage' }).catch(() => {});
      }
    }, 250);
  }

  onBackground(): void {
    AppLock.onBackground();
  }
}
