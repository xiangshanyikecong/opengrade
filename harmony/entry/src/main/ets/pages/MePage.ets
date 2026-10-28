// 账号设置(可复用视图 + 可路由页面):资料、改密、服务器管理、退出。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../common/OgNet';
import { OgSession } from '../common/OgSession';
import { OgC, OgDim } from '../common/OgTheme';
import { OgFormat } from '../common/OgFormat';
import { Me } from '../model/Api';
import { KVRow, SectionHeader } from '../components/Ui';

@Component
export struct MeView {
  @State oldPwd: string = '';
  @State newPwd: string = '';
  @State newPwd2: string = '';
  @State pwdMsg: string = '';
  @State busy: boolean = false;
  @State servers: string[] = [];
  @State newServer: string = '';
  @State me: Me | null = null;

  aboutToAppear(): void {
    this.me = OgSession.me();
    OgSession.servers().then((list) => {
      this.servers = list;
    });
  }

  private async doChangePwd(): Promise<void> {
    if (this.newPwd.length < 5) {
      this.pwdMsg = '新密码至少 5 位';
      return;
    }
    if (this.newPwd !== this.newPwd2) {
      this.pwdMsg = '两次输入的新密码不一致';
      return;
    }
    this.busy = true;
    this.pwdMsg = '';
    try {
      await OgNet.jpost<Record<string, string>>('/api/v1/auth/password', {
        'oldPassword': this.oldPwd,
        'newPassword': this.newPwd
      } as Record<string, Object>);
      this.oldPwd = '';
      this.newPwd = '';
      this.newPwd2 = '';
      promptAction.showToast({ message: '密码已修改' });
    } catch (e) {
      this.pwdMsg = e instanceof OgError ? e.message : '修改失败';
    }
    this.busy = false;
  }

  private doLogout(): void {
    OgNet.jpost<Record<string, string>>('/api/v1/auth/logout', null)
      .catch(() => {});
    OgSession.clearToken();
    router.clear();
    router.replaceUrl({ url: 'pages/LoginPage' }).catch(() => {});
  }

  private async switchServer(url: string): Promise<void> {
    if (url === OgSession.baseUrl()) {
      return;
    }
    // 切换服务器 = 切换学校:令牌按服务器隔离,必须重新登录。
    OgSession.clearToken();
    OgSession.setBaseUrl(url);
    router.clear();
    router.replaceUrl({ url: 'pages/LoginPage' }).catch(() => {});
  }

  private async addServer(): Promise<void> {
    const url = this.newServer.trim();
    if (url === '') {
      return;
    }
    try {
      await OgNet.checkServer(url);
      await OgSession.rememberServer(url);
      this.servers = await OgSession.servers();
      this.newServer = '';
      promptAction.showToast({ message: '已添加' });
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '校验失败' });
    }
  }

  build() {
    Scroll() {
      Column({ space: OgDim.GAP }) {
        // 资料
        Column({ space: 6 }) {
          Row({ space: 12 }) {
            Text(this.me !== null && this.me.displayName.length > 0 ? this.me.displayName.substring(0, 1) : '·')
              .fontSize(22)
              .fontColor(Color.White)
              .width(52)
              .height(52)
              .textAlign(TextAlign.Center)
              .backgroundColor(OgC.BRAND)
              .borderRadius(26)
            Column({ space: 2 }) {
              Text(this.me !== null ? this.me.displayName : '').fontSize(17).fontWeight(FontWeight.Bold).fontColor(OgC.INK)
              Text((this.me !== null ? this.me.username : '') + ' · ' +
                OgFormat.roleName(this.me !== null ? this.me.role : ''))
                .fontSize(12).fontColor(OgC.INK_SUB)
            }
            .alignItems(HorizontalAlign.Start)
            .layoutWeight(1)
          }
          .width('100%')
          if (this.me !== null && this.me.role === 'parent' && this.me.children !== undefined) {
            Text('绑定子女考号:' + this.me.children.join('、'))
              .fontSize(12).fontColor(OgC.INK_SUB)
          }
        }
        .width('100%')
        .padding(14)
        .backgroundColor(OgC.CARD)
        .borderRadius(OgDim.RADIUS)

        // 改密
        Column({ space: 8 }) {
          SectionHeader({ text: '修改密码' })
          TextInput({ text: this.oldPwd, placeholder: '当前密码' })
            .type(InputType.Password).fontSize(14).height(40)
            .onChange((v: string) => {
              this.oldPwd = v;
            })
          TextInput({ text: this.newPwd, placeholder: '新密码(至少 5 位)' })
            .type(InputType.Password).fontSize(14).height(40)
            .onChange((v: string) => {
              this.newPwd = v;
            })
          TextInput({ text: this.newPwd2, placeholder: '确认新密码' })
            .type(InputType.Password).fontSize(14).height(40)
            .onChange((v: string) => {
              this.newPwd2 = v;
            })
          if (this.pwdMsg !== '') {
            Text(this.pwdMsg).fontSize(12).fontColor(OgC.BAD)
          }
          Button(this.busy ? '提交中…' : '确认修改')
            .width('100%').height(40).fontSize(14)
            .backgroundColor(OgC.BRAND)
            .enabled(!this.busy)
            .onClick(() => this.doChangePwd())
        }
        .width('100%')
        .padding(14)
        .backgroundColor(OgC.CARD)
        .borderRadius(OgDim.RADIUS)
        .alignItems(HorizontalAlign.Start)

        // 服务器管理
        Column({ space: 8 }) {
          SectionHeader({ text: '学校服务器' })
          KVRow({ k: '当前', v: OgSession.baseUrl() })
          ForEach(this.servers, (s: string) => {
            Row({ space: 8 }) {
              Text(s).fontSize(13).fontColor(s === OgSession.baseUrl() ? OgC.BRAND : OgC.INK)
                .layoutWeight(1).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
              if (s === OgSession.baseUrl()) {
                Text('当前').fontSize(11).fontColor(OgC.BRAND)
              } else {
                Text('切换').fontSize(12).fontColor(OgC.BRAND).onClick(() => this.switchServer(s))
              }
            }
            .width('100%')
            .padding({ top: 6, bottom: 6 })
            .border({ width: { bottom: 1 }, color: OgC.LINE })
          }, (s: string) => s)
          Row({ space: 8 }) {
            TextInput({ text: this.newServer, placeholder: '添加新地址 http://…' })
              .fontSize(13).height(38).layoutWeight(1)
              .onChange((v: string) => {
                this.newServer = v;
              })
            Button('添加').fontSize(13).height(38).backgroundColor(OgC.BRAND_DEEP)
              .onClick(() => this.addServer())
          }
          .width('100%')
          Text('切换服务器会退出当前登录(账号按学校隔离)。')
            .fontSize(11).fontColor(OgC.INK_SUB)
        }
        .width('100%')
        .padding(14)
        .backgroundColor(OgC.CARD)
        .borderRadius(OgDim.RADIUS)
        .alignItems(HorizontalAlign.Start)

        Button('退出登录')
          .width('100%')
          .height(44)
          .fontSize(15)
          .backgroundColor(OgC.BAD_BG)
          .fontColor(OgC.BAD)
          .onClick(() => this.doLogout())

        Text('OpenGrade 开元智阅 · 鸿蒙客户端 v1.0.0\n服务端 ' + OgSession.baseUrl().replace(/^https?:\/\//, ''))
          .fontSize(11)
          .fontColor(OgC.INK_SUB)
          .textAlign(TextAlign.Center)
          .width('100%')
          .margin({ bottom: 20 })
      }
      .padding(OgDim.PAD)
      .width('100%')
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
    .scrollBar(BarState.Off)
    .align(Alignment.Top)
  }
}

@Entry
@Component
struct MePage {
  build() {
    Column() {
      TitleBarWrap()
      MeView()
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
  }
}

@Component
struct TitleBarWrap {
  build() {
    Row() {
      Text('‹')
        .fontSize(26).fontColor(OgC.BRAND).width(36).height(36).textAlign(TextAlign.Center)
        .onClick(() => router.back())
      Text('账号设置').fontSize(17).fontWeight(FontWeight.Bold).fontColor(OgC.INK)
      Blank()
    }
    .width('100%')
    .padding(12)
    .backgroundColor(OgC.CARD)
  }
}
