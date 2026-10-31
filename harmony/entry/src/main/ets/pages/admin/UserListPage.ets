// 用户管理(仅管理员):列表/搜索/新建/重置密码/删除。
// CSV 批量导入与角色细节维护在电脑端操作。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { PublicUser } from '../../model/Api';
import { EmptyView, ErrorView, LoadingView, Pill, PrimaryButton, TitleBar } from '../../components/Ui';
import { NAV_SHEET_HEIGHT, StaffNavGrid } from '../../components/StaffNav';

const ROLES: string[] = ['admin', 'leader', 'teacher', 'student', 'parent'];

@Entry
@Component
struct UserListPage {
  @State items: PublicUser[] = [];
  @State q: string = '';
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State navOpen: boolean = false;
  @State sheetKind: string = ''; // '' | nav | create | reset
  @State sheetOpen: boolean = false;
  @State resetUser: string = '';
  @State newUsername: string = '';
  @State newDisplay: string = '';
  @State newRole: string = 'teacher';
  @State newReg: string = '';
  @State newClass: string = '';
  @State newGrades: string = '';
  @State newChildren: string = '';
  @State newPassword: string = '';
  @State resetPwd: string = '';
  @State busy: boolean = false;

  aboutToAppear(): void {
    this.load();
  }

  private async load(): Promise<void> {
    this.loading = this.items.length === 0;
    this.failed = false;
    try {
      this.items = (await OgNet.jget<PublicUser[]>('/api/v1/users')) ?? [];
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  private filtered(): PublicUser[] {
    const kw = this.q.trim().toLowerCase();
    if (kw === '') {
      return this.items;
    }
    const out: PublicUser[] = [];
    for (const u of this.items) {
      const hay = (u.username + ' ' + u.displayName + ' ' + (u.reg ?? '') + ' ' + (u.class ?? '')
        + ' ' + (u.classes !== undefined ? u.classes.join('/') : '')).toLowerCase();
      if (hay.indexOf(kw) >= 0) {
        out.push(u);
      }
    }
    return out;
  }

  private openSheet(kind: string): void {
    this.sheetKind = kind;
    this.sheetOpen = true;
  }

  private closeSheet(): void {
    this.sheetOpen = false;
    this.sheetKind = '';
  }

  private async createUser(): Promise<void> {
    const username = this.newUsername.trim();
    if (username === '' || this.newPassword.length < 5) {
      promptAction.showToast({ message: '用户名必填,密码至少 5 位' });
      return;
    }
    this.busy = true;
    try {
      const body: Record<string, Object> = {
        'username': username,
        'displayName': this.newDisplay.trim(),
        'role': this.newRole,
        'password': this.newPassword
      };
      if (this.newReg.trim() !== '') {
        body['reg'] = this.newReg.trim();
      }
      if (this.newClass.trim() !== '') {
        body['class'] = this.newClass.trim();
      }
      if (this.newGrades.trim() !== '') {
        body['grades'] = this.newGrades.trim().split(/[|、,]/);
      }
      if (this.newChildren.trim() !== '') {
        body['children'] = this.newChildren.trim().split(/[|、,]/);
      }
      await OgNet.jpost<Record<string, string>>('/api/v1/users', body);
      promptAction.showToast({ message: '已创建 ' + username + '(首登将强制改密)' });
      this.closeSheet();
      this.newUsername = '';
      this.newDisplay = '';
      this.newReg = '';
      this.newClass = '';
      this.newGrades = '';
      this.newChildren = '';
      this.newPassword = '';
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '创建失败', duration: 3000 });
    }
    this.busy = false;
  }

  private async resetPassword(): Promise<void> {
    if (this.resetPwd.length < 5) {
      promptAction.showToast({ message: '新密码至少 5 位' });
      return;
    }
    this.busy = true;
    try {
      await OgNet.jpost<Record<string, string>>('/api/v1/users/' + this.resetUser + '/password', {
        'newPassword': this.resetPwd
      } as Record<string, string>);
      promptAction.showToast({ message: this.resetUser + ' 密码已重置(其会话已全部吊销)' });
      this.closeSheet();
      this.resetPwd = '';
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '重置失败' });
    }
    this.busy = false;
  }

  private confirmDelete(u: PublicUser): void {
    AlertDialog.show({
      message: '确认删除账号 ' + u.username + '('
        + (u.displayName !== '' ? u.displayName : u.username) + ')? 删除后该账号无法登录,考试与成绩数据不受影响。',
      title: '删除账号',
      autoCancel: true,
      alignment: DialogAlignment.Center,
      primaryButton: {
        value: '取消',
        action: () => {
        }
      },
      secondaryButton: {
        value: '确认删除',
        fontColor: OgC.BAD,
        action: () => {
          this.deleteUser(u);
        }
      }
    });
  }

  private async deleteUser(u: PublicUser): Promise<void> {
    this.busy = true;
    try {
      await OgNet.jdel<Record<string, string>>('/api/v1/users/' + u.username);
      promptAction.showToast({ message: '已删除 ' + u.username });
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '删除失败' });
    }
    this.busy = false;
  }

  @Builder
  userCard(u: PublicUser) {
    Column({ space: 6 }) {
      Row({ space: 8 }) {
        Text(u.displayName !== '' ? u.displayName : u.username)
          .fontSize(14).fontWeight(FontWeight.Medium).fontColor(OgC.INK).layoutWeight(1)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        Pill({
          text: OgFormat.roleName(u.role),
          color: u.role === 'admin' ? OgC.BAD : (u.role === 'leader' ? OgC.WARN : OgC.BRAND)
        })
      }
      .width('100%')
      Text(u.username + (u.reg !== undefined && u.reg !== '' ? ' · 考号 ' + u.reg : '')
        + (u.class !== undefined && u.class !== '' ? ' · ' + u.class : '')
        + (u.classes !== undefined && u.classes.length > 1 ? '/' + u.classes.join('/') : ''))
        .fontSize(11).fontColor(OgC.INK_SUB)
        .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
      Row({ space: 10 }) {
        Text('重置密码').fontSize(12).fontColor(OgC.BRAND)
          .onClick(() => {
            this.resetUser = u.username;
            this.resetPwd = '';
            this.openSheet('reset');
          })
        Blank()
        if (u.mustChangePassword === true) {
          Pill({ text: '未改初始密码', color: OgC.WARN })
        }
        Text('删除').fontSize(12).fontColor(OgC.BAD)
          .enabled(!this.busy)
          .onClick(() => this.confirmDelete(u))
      }
      .width('100%')
    }
    .width('100%')
    .padding(12)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
  }

  build() {
    Column() {
      TitleBar({
        title: '用户管理',
        showBack: false,
        actions: (): void => {
          this.navAction()
        }
      })
      Row({ space: 8 }) {
        Search({ value: this.q, placeholder: '搜索用户名/姓名/班级/考号' })
          .height(38).layoutWeight(1)
          .onChange((v: string) => {
            this.q = v;
          })
        PrimaryButton({
          label: '+ 新建',
          onTap: () => {
            this.openSheet('create');
          }
        })
      }
      .width('100%')
      .padding({ left: OgDim.PAD, right: OgDim.PAD, top: 8, bottom: 4 })

      Scroll() {
        Column({ space: OgDim.GAP }) {
          if (this.loading) {
            LoadingView()
          } else if (this.failed) {
            ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
          } else if (this.filtered().length === 0) {
            EmptyView({ text: '没有匹配的账号' })
          }
          ForEach(this.filtered(), (u: PublicUser) => {
            this.userCard(u)
          }, (u: PublicUser) => u.username + String(u.mustChangePassword))
          Text('CSV 批量导入与多班/学科等细节维护请在电脑端用户管理页操作。')
            .fontSize(11).fontColor(OgC.INK_SUB)
            .width('100%')
            .textAlign(TextAlign.Center)
          Text('').height(20)
        }
        .padding(OgDim.PAD)
        .width('100%')
      }
      .layoutWeight(1)
      .scrollBar(BarState.Off)
      .align(Alignment.Top)
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
    .bindSheet($$this.sheetOpen, this.anySheetBuilder(), {
      // 导航分支与其他页同高;建号/改密表单分支自适应
      height: this.sheetKind === 'nav' ? NAV_SHEET_HEIGHT : SheetSize.FIT_CONTENT,
      dragBar: true
    })
  }

  @Builder
  navAction() {
    Text('☰').fontSize(20).fontColor(OgC.BRAND).padding({ left: 10, right: 4 })
      .onClick(() => {
        this.openSheet('nav');
      })
  }

  @Builder
  anySheetBuilder() {
    if (this.sheetKind === 'nav') {
      StaffNavGrid({ current: 'users', onClose: () => {
        this.closeSheet();
      } })
    } else if (this.sheetKind === 'create') {
      this.createSheet()
    } else if (this.sheetKind === 'reset') {
      this.resetSheet()
    }
  }

  @Builder
  createSheet() {
    Scroll() {
      Column({ space: 8 }) {
        TextInput({ text: this.newUsername, placeholder: '用户名 *' })
          .fontSize(14).height(40)
          .onChange((v: string) => {
            this.newUsername = v;
          })
        TextInput({ text: this.newDisplay, placeholder: '姓名' })
          .fontSize(14).height(40)
          .onChange((v: string) => {
            this.newDisplay = v;
          })
        Row({ space: 6 }) {
          ForEach(ROLES, (r: string) => {
            Text(OgFormat.roleName(r))
              .fontSize(12)
              .fontColor(this.newRole === r ? Color.White : OgC.INK)
              .backgroundColor(this.newRole === r ? OgC.BRAND : OgC.BG)
              .padding({ left: 10, right: 10, top: 6, bottom: 6 })
              .borderRadius(12)
              .onClick(() => {
                this.newRole = r;
              })
          }, (r: string) => r)
        }
        .width('100%')
        TextInput({ text: this.newReg, placeholder: '考号(学生)' })
          .fontSize(14).height(40)
          .onChange((v: string) => {
            this.newReg = v;
          })
        TextInput({ text: this.newClass, placeholder: '班级(学生/教师,多个用 | )' })
          .fontSize(14).height(40)
          .onChange((v: string) => {
            this.newClass = v;
          })
        TextInput({ text: this.newGrades, placeholder: '管辖年级(组长,多个用 | )' })
          .fontSize(14).height(40)
          .onChange((v: string) => {
            this.newGrades = v;
          })
        TextInput({ text: this.newChildren, placeholder: '绑定子女考号(家长,多个用 | )' })
          .fontSize(14).height(40)
          .onChange((v: string) => {
            this.newChildren = v;
          })
        TextInput({ text: this.newPassword, placeholder: '初始密码(至少 5 位,首登强制改密)' })
          .fontSize(14).height(40)
          .onChange((v: string) => {
            this.newPassword = v;
          })
        PrimaryButton({
          full: true,
          label: this.busy ? '创建中…' : '创建账号',
          usable: !this.busy,
          onTap: () => this.createUser()
        })
      }
      .padding(OgDim.PAD)
      .width('100%')
    }
    .height(420)
  }

  @Builder
  resetSheet() {
    Column({ space: 10 }) {
      Text('为 ' + this.resetUser + ' 设置新密码(该账号所有会话将被吊销,首登强制改密)。')
        .fontSize(12).fontColor(OgC.INK_SUB)
      TextInput({ text: this.resetPwd, placeholder: '新密码(至少 5 位)' })
        .fontSize(14).height(40)
        .onChange((v: string) => {
          this.resetPwd = v;
        })
      PrimaryButton({
        full: true,
        label: this.busy ? '重置中…' : '确认重置',
        usable: !this.busy,
        onTap: () => this.resetPassword()
      })
    }
    .padding(OgDim.PAD)
    .width('100%')
  }
}
