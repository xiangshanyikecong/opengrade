// 路由分发(启动页):初始化会话 → 应用深色档位 → 应用锁门禁 → 按角色落地。
//   未登录 → 登录页;强制改密 → 改密页;
//   student/parent → 学生端;teacher → 工作台。

import { router } from '@kit.ArkUI';
import { OgSession } from '../common/OgSession';
import { AppLock } from '../common/AppLock';
import { OgC } from '../common/OgTheme';

@Entry
@Component
struct Index {
  build() {
    Column({ space: 14 }) {
      Image($r('app.media.app_icon'))
        .width(88)
        .height(88)
        .borderRadius(20)
      Text('开元智阅').fontSize(22).fontWeight(FontWeight.Bold).fontColor(OgC.INK)
      Text('把成绩还给教育,让阅卷回归教学').fontSize(13).fontColor(OgC.INK_SUB)
      LoadingProgress().width(28).height(28).color(OgC.BRAND).margin({ top: 16 })
    }
    .width('100%')
    .height('100%')
    .justifyContent(FlexAlign.Center)
    .backgroundColor(OgC.BG)
  }

  aboutToAppear(): void {
    OgSession.init(getContext(this))
      .then(async () => {
        await AppLock.ensure(getContext(this));
        // 深色档位:auto=跟随系统(EntryAbility 已同步系统值)
        OgC.setMode(OgSession.themeMode());
        // 目标页
        let target = 'pages/teacher/HomePage';
        if (!OgSession.loggedIn()) {
          target = 'pages/LoginPage';
        } else {
          const me = OgSession.me();
          if (me !== null && me.mustChangePassword) {
            target = 'pages/ForcePwdPage';
          } else if (OgSession.isStudentish()) {
            target = 'pages/student/StudentHomePage';
          }
        }
        // 应用锁:开锁后直达目标页(避免过 splash 二跳)
        if (target !== 'pages/LoginPage' && AppLock.enabled()) {
          router.replaceUrl({
            url: 'pages/common/LockPage',
            params: { 'next': target } as Record<string, string>
          }).catch(() => {});
          return;
        }
        router.replaceUrl({ url: target }).catch(() => {});
      })
      .catch(() => {
        router.replaceUrl({ url: 'pages/LoginPage' }).catch(() => {});
      });
  }
}
