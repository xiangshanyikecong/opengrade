// 首登强制改密页:服务端闸门(mustChangePassword)要求改密前只放行认证端点。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../common/OgNet';
import { OgSession } from '../common/OgSession';
import { OgC } from '../common/OgTheme';
import { Me } from '../model/Api';

@Entry
@Component
struct ForcePwdPage {
  @State oldPwd: string = '';
  @State newPwd: string = '';
  @State newPwd2: string = '';
  @State msg: string = '';
  @State busy: boolean = false;

  private async doChange(): Promise<void> {
    if (this.newPwd.length < 5) {
      this.msg = '新密码至少 5 位';
      return;
    }
    if (this.newPwd !== this.newPwd2) {
      this.msg = '两次输入的新密码不一致';
      return;
    }
    this.busy = true;
    this.msg = '';
    try {
      await OgNet.jpost<Record<string, string>>('/api/v1/auth/password', {
        'oldPassword': this.oldPwd,
        'newPassword': this.newPwd
      } as Record<string, Object>);
      const me = await OgNet.jget<Me>('/api/v1/auth/me');
      OgSession.setMe(me);
      promptAction.showToast({ message: '密码已修改' });
      if (OgSession.isStudentish()) {
        router.replaceUrl({ url: 'pages/student/StudentHomePage' }).catch(() => {});
      } else if (me.role === 'teacher') {
        router.replaceUrl({ url: 'pages/teacher/TaskListPage' }).catch(() => {});
      } else {
        router.replaceUrl({ url: 'pages/teacher/ExamListPage' }).catch(() => {});
      }
    } catch (e) {
      this.msg = e instanceof OgError ? e.message : '修改失败';
    }
    this.busy = false;
  }

  build() {
    Column({ space: 14 }) {
      Text('修改初始密码').fontSize(20).fontWeight(FontWeight.Bold).fontColor(OgC.INK).margin({ top: 60 })
      Text('当前账号仍在使用初始密码,修改后才能使用系统其它功能。')
        .fontSize(13).fontColor(OgC.INK_SUB).textAlign(TextAlign.Center)
      TextInput({ text: this.oldPwd, placeholder: '原密码(初始密码)' })
        .type(InputType.Password)
        .fontSize(15)
        .height(44)
        .onChange((v: string) => {
          this.oldPwd = v;
        })
      TextInput({ text: this.newPwd, placeholder: '新密码(至少 5 位)' })
        .type(InputType.Password)
        .fontSize(15)
        .height(44)
        .onChange((v: string) => {
          this.newPwd = v;
        })
      TextInput({ text: this.newPwd2, placeholder: '确认新密码' })
        .type(InputType.Password)
        .fontSize(15)
        .height(44)
        .onChange((v: string) => {
          this.newPwd2 = v;
        })
      if (this.msg !== '') {
        Text(this.msg).fontSize(13).fontColor(OgC.BAD)
      }
      Button(this.busy ? '提交中…' : '确认修改')
        .width('100%')
        .height(46)
        .fontSize(16)
        .backgroundColor(OgC.BRAND)
        .enabled(!this.busy)
        .onClick(() => this.doChange())
    }
    .padding({ left: 24, right: 24 })
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
  }
}
