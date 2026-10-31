// 账号设置(可复用视图 + 可路由页面):
//   资料、学校服务器(含延迟测速)、诊断(报错统计/省流模式/清理缓存)、改密、退出。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError, OgStats } from '../common/OgNet';
import { OgSession } from '../common/OgSession';
import { AppLock } from '../common/AppLock';
import { OgCache } from '../common/OgCache';
import { OgC, OgDim } from '../common/OgTheme';
import { OgFormat } from '../common/OgFormat';
import { Me } from '../model/Api';
import { GhostButton, KVRow, Pill, PrimaryButton, SectionHeader } from '../components/Ui';

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
  @State lat: Record<string, number> = {}; // url → ms(-1 不可达)
  @State pinging: boolean = false;
  @State statsTick: number = 0;
  @State lowData: boolean = false;
  // 深色模式
  @State themeMode: string = 'auto';
  // 应用锁
  @State lockOn: boolean = false;
  @State lockBio: boolean = false;
  @State lockCanBio: boolean = false;
  @State lockSetup: boolean = false; // 展开设置 PIN 表单
  @State lockPin1: string = '';
  @State lockPin2: string = '';
  @State lockVerify: string = ''; // 关闭锁时验证
  @State lockMsg: string = '';
  @State lockBusy: boolean = false;

  aboutToAppear(): void {
    this.me = OgSession.me();
    this.lowData = OgSession.lowData();
    this.themeMode = OgSession.themeMode();
    this.refreshLock();
    OgSession.servers().then((list) => {
      this.servers = list;
    });
  }

  private refreshLock(): void {
    this.lockOn = AppLock.enabled();
    this.lockBio = AppLock.biometricOn();
    this.lockCanBio = AppLock.canBiometric();
  }

  private setTheme(mode: string): void {
    this.themeMode = mode;
    OgSession.setThemeMode(mode);
    OgC.setMode(mode);
    promptAction.showToast({ message: '深色模式已切换,页面刷新后全部生效' });
  }

  private async enableLock(): Promise<void> {
    const p1 = this.lockPin1.trim();
    const p2 = this.lockPin2.trim();
    if (!(p1.length >= 4 && p1.length <= 6) || !/^[0-9]+$/.test(p1)) {
      this.lockMsg = 'PIN 须为 4~6 位数字';
      return;
    }
    if (p1 !== p2) {
      this.lockMsg = '两次输入的 PIN 不一致';
      return;
    }
    this.lockBusy = true;
    await AppLock.ensure(OgSession.context());
    await AppLock.setPin(p1);
    if (this.lockCanBio) {
      await AppLock.setBiometric(this.lockBio);
    }
    this.lockBusy = false;
    this.lockSetup = false;
    this.lockPin1 = '';
    this.lockPin2 = '';
    this.lockMsg = '';
    this.refreshLock();
    promptAction.showToast({ message: '应用锁已开启,退到后台再回来需要解锁' });
  }

  private async disableLock(): Promise<void> {
    if (this.lockVerify.trim() === '') {
      this.lockMsg = '请输入当前 PIN 确认关闭';
      return;
    }
    this.lockBusy = true;
    const ok = await AppLock.verify(this.lockVerify.trim());
    if (!ok) {
      this.lockBusy = false;
      this.lockMsg = 'PIN 不正确';
      return;
    }
    await AppLock.clearPin();
    this.lockBusy = false;
    this.lockVerify = '';
    this.lockMsg = '';
    this.refreshLock();
    promptAction.showToast({ message: '应用锁已关闭' });
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

  // 对当前 + 已存服务器逐个测速(GET /healthz 往返毫秒)。
  private async measureLatency(): Promise<void> {
    this.pinging = true;
    const targets: string[] = [];
    const cur = OgSession.baseUrl();
    if (cur !== '') {
      targets.push(cur);
    }
    for (const s of this.servers) {
      if (s !== cur && targets.indexOf(s) < 0) {
        targets.push(s);
      }
    }
    const result: Record<string, number> = {};
    for (const t of targets) {
      const ms = await OgNet.ping(t);
      result[t] = ms;
      this.lat = result; // 逐个回填,界面即时刷新
    }
    this.pinging = false;
    this.statsTick++;
  }

  private latText(url: string): string {
    const v = this.lat[url];
    if (v === undefined) {
      return '未测';
    }
    if (v < 0) {
      return '不可达';
    }
    return String(v) + 'ms';
  }

  private latColor(url: string): string {
    const v = this.lat[url];
    if (v === undefined) {
      return OgC.INK_SUB;
    }
    if (v < 0) {
      return OgC.BAD;
    }
    if (v < 100) {
      return OgC.GOOD;
    }
    if (v < 500) {
      return OgC.WARN;
    }
    return OgC.BAD;
  }

  private async doClearCache(): Promise<void> {
    OgNet.clearAppCache();
    OgCache.clear();
    promptAction.showToast({ message: '缓存已清理(图片内存/磁盘缓存 + 离线数据 + 沙箱临时文件)' });
  }

  private toggleLowData(on: boolean): void {
    OgSession.setLowData(on);
    this.lowData = on;
    if (on) {
      OgNet.clearAppCache(); // 切入省流:顺手清掉高分辨率位图缓存
    }
    promptAction.showToast({ message: on ? '省流模式已开启:图片降采样、关闭阅卷预加载' : '省流模式已关闭' });
  }

  @Builder
  latencyCell(url: string) {
    Text(this.latText(url))
      .fontSize(11)
      .fontColor(this.latColor(url))
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

        // 学校服务器(含延迟)
        Column({ space: 8 }) {
          Row({ space: 8 }) {
            // SectionHeader 内部是 width('100%'),直接放 Row 会占满整行把「测速」挤出屏幕,必须限宽
            Column() {
              SectionHeader({ text: '学校服务器' })
            }
            .layoutWeight(1)
            Text(this.pinging ? '测速中…' : '测速')
              .fontSize(12)
              .fontColor(OgC.BRAND)
              .enabled(!this.pinging)
              .opacity(this.pinging ? 0.5 : 1)
              .onClick(() => this.measureLatency())
          }
          .width('100%')
          KVRow({ k: '当前', v: OgSession.baseUrl() })
          ForEach(this.servers, (s: string) => {
            Row({ space: 8 }) {
              Column({ space: 2 }) {
                Text(s).fontSize(13).fontColor(s === OgSession.baseUrl() ? OgC.BRAND : OgC.INK)
                  .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
                this.latencyCell(s)
              }
              .alignItems(HorizontalAlign.Start)
              .layoutWeight(1)
              if (s === OgSession.baseUrl()) {
                Text('当前').fontSize(11).fontColor(OgC.BRAND)
              } else {
                Text('切换').fontSize(12).fontColor(OgC.BRAND).onClick(() => this.switchServer(s))
              }
            }
            .width('100%')
            .padding({ top: 6, bottom: 6 })
            .border({ width: { bottom: 1 }, color: OgC.LINE })
          }, (s: string) => s + this.latText(s))
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
          Text('延迟为 GET /healthz 往返耗时;切换服务器会退出当前登录(账号按学校隔离)。')
            .fontSize(11).fontColor(OgC.INK_SUB)
        }
        .width('100%')
        .padding(14)
        .backgroundColor(OgC.CARD)
        .borderRadius(OgDim.RADIUS)
        .alignItems(HorizontalAlign.Start)

        // 诊断:报错统计 / 省流模式 / 清理缓存
        Column({ space: 8 }) {
          SectionHeader({ text: '诊断', extra: '本机自上次启动以来' })
          KVRow({ k: '网络异常(连不上/超时)', v: String(OgStats.netErr) + ' 次', vColor: OgStats.netErr > 0 ? OgC.WARN : OgC.INK_SUB })
          KVRow({ k: '请求错误(4xx/5xx)', v: String(OgStats.httpErr) + ' 次', vColor: OgStats.httpErr > 0 ? OgC.WARN : OgC.INK_SUB })
          if (OgStats.lastMsg !== '') {
            Text('最近一次:' + OgStats.lastMsg + '(' + OgStats.lastAt + ')')
              .fontSize(11).fontColor(OgC.INK_SUB)
          }
          Row({ space: 8 }) {
            Text('省流模式')
              .fontSize(13).fontColor(OgC.INK).layoutWeight(1)
            Toggle({ type: ToggleType.Switch, isOn: this.lowData })
              .selectedColor(OgC.BRAND)
              .onChange((on: boolean) => this.toggleLowData(on))
          }
          .width('100%')
          Text('开启后图片降采样至 1200px、关闭阅卷切片预加载,弱网更流畅。')
            .fontSize(11).fontColor(OgC.INK_SUB)
          Button('清理缓存')
            .width('100%')
            .height(38)
            .fontSize(13)
            .backgroundColor(OgC.BG)
            .fontColor(OgC.INK)
            .onClick(() => this.doClearCache())
        }
        .width('100%')
        .padding(14)
        .backgroundColor(OgC.CARD)
        .borderRadius(OgDim.RADIUS)
        .alignItems(HorizontalAlign.Start)

        // 深色模式
        Column({ space: 8 }) {
          SectionHeader({ text: '深色模式' })
          Row({ space: 8 }) {
            ForEach(['auto', 'light', 'dark'], (m: string) => {
              Text(m === 'auto' ? '跟随系统' : (m === 'light' ? '浅色' : '深色'))
                .fontSize(12)
                .fontColor(this.themeMode === m ? Color.White : OgC.INK)
                .backgroundColor(this.themeMode === m ? OgC.BRAND : OgC.BG)
                .padding({ left: 12, right: 12, top: 6, bottom: 6 })
                .borderRadius(12)
                .onClick(() => this.setTheme(m))
            }, (m: string) => m + this.themeMode)
            Blank()
          }
          .width('100%')
          Text('跟随系统时,系统切换深色后新打开的页面即变深色;已停留页面的配色在下次进入时刷新。')
            .fontSize(11).fontColor(OgC.INK_SUB)
        }
        .width('100%')
        .padding(14)
        .backgroundColor(OgC.CARD)
        .borderRadius(OgDim.RADIUS)
        .alignItems(HorizontalAlign.Start)

        // 应用锁
        Column({ space: 8 }) {
          SectionHeader({ text: '应用锁', extra: '本机 PIN / 生物识别' })
          Row({ space: 8 }) {
            Text('打开应用时需要解锁').fontSize(13).fontColor(OgC.INK).layoutWeight(1)
            Pill({ text: this.lockOn ? '已开启' : '未开启', color: this.lockOn ? OgC.GOOD : OgC.INK_SUB })
          }
          .width('100%')
          if (!this.lockOn && !this.lockSetup) {
            Text('开启应用锁(设置 4~6 位 PIN)')
              .fontSize(13)
              .fontColor(OgC.BRAND)
              .onClick(() => {
                this.lockSetup = true;
                this.lockMsg = '';
              })
          }
          if (this.lockSetup) {
            TextInput({ text: this.lockPin1, placeholder: 'PIN(4~6 位数字)' })
              .type(InputType.Number)
              .fontSize(14).height(40)
              .onChange((v: string) => {
                this.lockPin1 = v;
              })
            TextInput({ text: this.lockPin2, placeholder: '确认 PIN' })
              .type(InputType.Number)
              .fontSize(14).height(40)
              .onChange((v: string) => {
                this.lockPin2 = v;
              })
            if (this.lockCanBio) {
              Row({ space: 8 }) {
                Text('同时允许指纹/人脸解锁').fontSize(13).fontColor(OgC.INK).layoutWeight(1)
                Toggle({ type: ToggleType.Switch, isOn: this.lockBio })
                  .selectedColor(OgC.BRAND)
                  .onChange((on: boolean) => {
                    this.lockBio = on;
                  })
              }
              .width('100%')
            } else {
              Text('本机未检测到可用指纹/人脸,仅 PIN 解锁。')
                .fontSize(11).fontColor(OgC.INK_SUB)
            }
            Row({ space: 10 }) {
              PrimaryButton({
                label: this.lockBusy ? '开启中…' : '确认开启',
                fill: true,
                usable: !this.lockBusy,
                onTap: () => this.enableLock()
              })
              GhostButton({ label: '取消', onTap: () => {
                this.lockSetup = false;
                this.lockPin1 = '';
                this.lockPin2 = '';
                this.lockMsg = '';
              } })
            }
            .width('100%')
          }
          if (this.lockOn) {
            if (this.lockCanBio) {
              Row({ space: 8 }) {
                Text('生物识别解锁(指纹/人脸)').fontSize(13).fontColor(OgC.INK).layoutWeight(1)
                Toggle({ type: ToggleType.Switch, isOn: this.lockBio })
                  .selectedColor(OgC.BRAND)
                  .onChange((on: boolean) => {
                    AppLock.setBiometric(on);
                    this.lockBio = on;
                  })
              }
              .width('100%')
            }
            TextInput({ text: this.lockVerify, placeholder: '输入当前 PIN 关闭应用锁' })
              .type(InputType.Number)
              .fontSize(14).height(40)
              .onChange((v: string) => {
                this.lockVerify = v;
              })
            PrimaryButton({
              label: this.lockBusy ? '处理中…' : '关闭应用锁',
              full: true,
              usable: !this.lockBusy,
              onTap: () => this.disableLock()
            })
          }
          if (this.lockMsg !== '') {
            Text(this.lockMsg).fontSize(12).fontColor(OgC.BAD)
          }
          Text('PIN 仅保存在本机(SHA-256 后存储),生物识别走系统认证,不上传任何数据。')
            .fontSize(11).fontColor(OgC.INK_SUB)
        }
        .width('100%')
        .padding(14)
        .backgroundColor(OgC.CARD)
        .borderRadius(OgDim.RADIUS)
        .alignItems(HorizontalAlign.Start)

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
      MeView()
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
  }
}
