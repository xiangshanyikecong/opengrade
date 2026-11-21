// 登录页:服务器地址(可校验/记住多个)+ 账号密码 + native 通道。
// 登录成功后补拉 /auth/me 组装会话,再按角色路由。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../common/OgNet';
import { OgSession } from '../common/OgSession';
import { OgC, OgDim } from '../common/OgTheme';
import { LoginResp, Me } from '../model/Api';

@Entry
@Component
struct LoginPage {
  @State server: string = '';
  @State username: string = '';
  @State password: string = '';
  @State msg: string = '';
  @State busy: boolean = false;
  @State servers: string[] = [];
  @State checking: boolean = false;
  @State serverOk: string = '';

  aboutToAppear(): void {
    OgSession.init(getContext(this)).then(() => {
      this.server = OgSession.baseUrl();
      OgSession.servers().then((list) => {
        this.servers = list;
      });
    });
  }

  private async doCheck(): Promise<void> {
    const url = this.server.trim();
    if (url === '') {
      this.msg = '请填写服务器地址,如 http://192.168.1.10:8787';
      return;
    }
    this.checking = true;
    this.msg = '';
    try {
      const ver = await OgNet.checkServer(url);
      this.serverOk = '已连接 OpenGrade 服务 ' + (ver === '' ? '' : 'v' + ver);
      OgSession.setBaseUrl(url);
    } catch (e) {
      this.serverOk = '';
      this.msg = e instanceof OgError ? e.message : '连接失败';
    }
    this.checking = false;
  }

  private async doLogin(): Promise<void> {
    const url = this.server.trim();
    const user = this.username.trim();
    if (url === '') {
      this.msg = '请先填写服务器地址';
      return;
    }
    if (user === '' || this.password === '') {
      this.msg = '请输入用户名与密码';
      return;
    }
    this.busy = true;
    this.msg = '';
    try {
      const resp = await OgNet.nativeLogin(url, user, this.password);
      // 补拉 /me 组装完整会话(reg/class/children 等)
      OgSession.setBaseUrl(url);
      OgSession.setLogin(url, resp.token ?? '', {
        username: resp.username,
        displayName: resp.displayName,
        role: resp.role,
        mustChangePassword: resp.mustChangePassword
      });
      const me = await OgNet.jget<Me>('/api/v1/auth/me');
      OgSession.setMe(me);
      await OgSession.rememberServer(url);
      this.servers = await OgSession.servers();
      promptAction.showToast({ message: '欢迎,' + me.displayName });
      if (me.mustChangePassword) {
        router.replaceUrl({ url: 'pages/ForcePwdPage' }).catch(() => {});
      } else if (OgSession.isStudentish()) {
        router.replaceUrl({ url: 'pages/student/StudentHomePage' }).catch(() => {});
      } else {
        router.replaceUrl({ url: 'pages/teacher/HomePage' }).catch(() => {});
      }
    } catch (e) {
      this.msg = e instanceof OgError ? e.message : '登录失败';
    }
    this.busy = false;
  }

  build() {
    Scroll() {
      Column({ space: 14 }) {
        Image($r('app.media.app_icon')).width(72).height(72).borderRadius(16).margin({ top: 40 })
        Text('OpenGrade 开元智阅').fontSize(20).fontWeight(FontWeight.Bold).fontColor(OgC.INK)
        Text('把成绩还给教育,让阅卷回归教学').fontSize(12).fontColor(OgC.INK_SUB)

        Column({ space: 8 }) {
          Text('学校服务器').fontSize(12).fontColor(OgC.INK_SUB).width('100%')
          Row({ space: 8 }) {
            TextInput({ text: this.server, placeholder: 'http://192.168.1.10:8787' })
              .fontSize(14)
              .height(42)
              .layoutWeight(1)
              .onChange((v: string) => {
                this.server = v;
                this.serverOk = '';
              })
            Button(this.checking ? '…' : '检测')
              .fontSize(13)
              .height(42)
              .backgroundColor(OgC.BRAND_DEEP)
              .enabled(!this.checking)
              .onClick(() => this.doCheck())
          }
          .width('100%')
          if (this.serverOk !== '') {
            Text(this.serverOk).fontSize(12).fontColor(OgC.GOOD).width('100%')
          }
          Text('模拟器/手机请填电脑的局域网 IP(如 http://192.168.1.10:8787),不能用 127.0.0.1')
            .fontSize(11).fontColor(OgC.INK_SUB).width('100%')
          if (this.servers.length > 0) {
            Flex({ wrap: FlexWrap.Wrap }) {
              ForEach(this.servers, (s: string) => {
                Text(s.replace(/^https?:\/\//, ''))
                  .fontSize(11)
                  .fontColor(OgC.BRAND)
                  .border({ width: 1, color: OgC.BRAND, radius: 10 })
                  .padding({ left: 8, right: 8, top: 3, bottom: 3 })
                  .margin({ right: 6, bottom: 4 })
                  .onClick(() => {
                    this.server = s;
                    this.serverOk = '';
                  })
              }, (s: string) => s)
            }
            .width('100%')
          }
        }
        .alignItems(HorizontalAlign.Start)
        .width('100%')

        TextInput({ text: this.username, placeholder: '用户名' })
          .fontSize(15)
          .height(44)
          .onChange((v: string) => {
            this.username = v;
          })
        TextInput({ text: this.password, placeholder: '密码' })
          .type(InputType.Password)
          .fontSize(15)
          .height(44)
          .onChange((v: string) => {
            this.password = v;
          })
          .onSubmit(() => this.doLogin())

        if (this.msg !== '') {
          Text(this.msg).fontSize(13).fontColor(OgC.BAD).width('100%')
        }

        Button(this.busy ? '登录中…' : '登 录')
          .width('100%')
          .height(46)
          .fontSize(16)
          .backgroundColor(OgC.BRAND)
          .enabled(!this.busy)
          .onClick(() => this.doLogin())

        Text('首次部署默认管理员 admin/admin123,登录后请立即修改密码。\n数据保存在学校自己的服务器上,本应用不做任何云端上报。')
          .fontSize(11)
          .fontColor(OgC.INK_SUB)
          .width('100%')
          .textAlign(TextAlign.Center)
          .margin({ top: 6, bottom: 30 })
      }
      .padding({ left: 24, right: 24 })
      .width('100%')
      .constraintSize({ maxWidth: 520 })
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
    .scrollBar(BarState.Off)
    .align(Alignment.Top)
  }
}
