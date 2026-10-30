// 路由分发(启动页):初始化会话 → 按角色落地。
//   未登录 → 登录页;强制改密 → 改密页;
//   student/parent → 学生端;teacher → 我的任务;admin/leader → 考试列表。

import { router } from '@kit.ArkUI';
import { OgSession } from '../common/OgSession';
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
      .then(() => {
        if (!OgSession.loggedIn()) {
          router.replaceUrl({ url: 'pages/LoginPage' }).catch(() => {});
          return;
        }
        const me = OgSession.me();
        if (me !== null && me.mustChangePassword) {
          router.replaceUrl({ url: 'pages/ForcePwdPage' }).catch(() => {});
          return;
        }
        if (OgSession.isStudentish()) {
          router.replaceUrl({ url: 'pages/student/StudentHomePage' }).catch(() => {});
        } else {
          router.replaceUrl({ url: 'pages/teacher/HomePage' }).catch(() => {});
        }
      })
      .catch(() => {
        router.replaceUrl({ url: 'pages/LoginPage' }).catch(() => {});
      });
  }
}
