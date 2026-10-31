// 教职员侧栏导航(☰ 弹出):所有工作台页共用,点击跳转对应页。
// current 标识当前页(置灰不跳);adminOnly 项仅管理员可见。

import { router } from '@kit.ArkUI';
import { OgSession } from '../common/OgSession';
import { OgC, OgDim } from '../common/OgTheme';
import { OgBreakpoint } from '../common/OgBreakpoint';

export interface NavItem {
  key: string;
  label: string;
  url: string;
  adminOnly: boolean;
  managerOnly?: boolean; // 管理员/年级组长(服务端 manage 闸门对齐)
}

export const STAFF_NAV: NavItem[] = [
  { key: 'home', label: '总览', url: 'pages/teacher/HomePage', adminOnly: false },
  { key: 'todo', label: '待办中心', url: 'pages/teacher/TodoPage', adminOnly: false },
  { key: 'tasks', label: '我的任务', url: 'pages/teacher/TaskListPage', adminOnly: false },
  { key: 'exams', label: '考试列表', url: 'pages/teacher/ExamListPage', adminOnly: false },
  { key: 'create', label: '新建考试', url: 'pages/teacher/CreateExamPage', adminOnly: false },
  { key: 'scanbuild', label: '拍卷建考', url: 'pages/teacher/LayoutScanPage', adminOnly: false },
  { key: 'assign', label: '任务分配', url: 'pages/teacher/AssignPage', adminOnly: false, managerOnly: true },
  { key: 'trends', label: '纵向趋势', url: 'pages/teacher/StaffTrendsPage', adminOnly: false },
  { key: 'aggregate', label: '年级总表', url: 'pages/teacher/AggregatePage', adminOnly: false },
  { key: 'rooms', label: '考场编排', url: 'pages/teacher/RoomsPage', adminOnly: false },
  { key: 'workload', label: '工作量', url: 'pages/teacher/WorkloadPage', adminOnly: false },
  { key: 'bank', label: '校本题库', url: 'pages/teacher/BankListPage', adminOnly: false },
  { key: 'users', label: '用户管理', url: 'pages/admin/UserListPage', adminOnly: true },
  { key: 'me', label: '账号设置', url: 'pages/MePage', adminOnly: false }
];

// 工作台弹层统一配置:各页 bindSheet 一律经 navSheetOptions()/formSheetOptions() 取参,
// 避免多处复制配置漂移;md/lg 网格 4 列更矮,并定宽收口不铺满内屏。
// 注意:本 SDK 的 SheetOptions.width 类型是 Dimension(仅 number/Resource),
// '72%'/'100%' 字符串编译不过 → 宽度只能定宽数值,手机档(sm)不传 width 走默认全宽。
export const NAV_SHEET_HEIGHT: number = 460;
export const NAV_SHEET_WIDE: number = 520;

// 导航弹层:sm 全宽原高;md/lg 定宽收口。title 传 '' 不设 title(ExamHub/UserList 混合弹层用)。
export function navSheetOptions(bp: string, title: string): SheetOptions {
  if (bp === 'sm') {
    return title === '' ? { height: NAV_SHEET_HEIGHT, dragBar: true }
      : { height: NAV_SHEET_HEIGHT, dragBar: true, title: { title: title } };
  }
  return title === '' ? { height: navSheetHeight(bp), width: NAV_SHEET_WIDE, dragBar: true }
    : { height: navSheetHeight(bp), width: NAV_SHEET_WIDE, dragBar: true, title: { title: title } };
}

// 表单类弹层(FIT_CONTENT 高度自适应):宽屏定宽收口,手机档默认全宽。
export function formSheetOptions(title: string): SheetOptions {
  const bp = OgBreakpoint.now();
  if (bp === 'sm') {
    return title === '' ? { height: SheetSize.FIT_CONTENT, dragBar: true }
      : { height: SheetSize.FIT_CONTENT, dragBar: true, title: { title: title } };
  }
  return title === '' ? { height: SheetSize.FIT_CONTENT, width: NAV_SHEET_WIDE, dragBar: true }
    : { height: SheetSize.FIT_CONTENT, width: NAV_SHEET_WIDE, dragBar: true, title: { title: title } };
}

// 弹层高度:sm 维持原 460;md/lg 4 列 4 行网格(ceil(14/4)*62=248)+ 标题栏/拖条留量。
export function navSheetHeight(bp: string): number {
  return bp === 'sm' ? NAV_SHEET_HEIGHT : 400;
}

@Component
export struct StaffNavGrid {
  current: string = '';
  onClose: () => void = () => {};
  @StorageProp('ogBp') bp: string = 'sm';

  private cols(): number {
    return this.bp === 'sm' ? 3 : 4; // 宽屏弹层有富余宽度,4 列少翻屏
  }

  private locked(item: NavItem): boolean {
    if (item.adminOnly && !OgSession.isAdmin()) {
      return true;
    }
    if (item.managerOnly === true && !OgSession.isManager()) {
      return true;
    }
    return false;
  }

  private go(item: NavItem): void {
    if (item.key === this.current) {
      this.onClose();
      return;
    }
    this.onClose();
    router.pushUrl({ url: item.url }).catch(() => {});
  }

  build() {
    Grid() {
      ForEach(STAFF_NAV, (item: NavItem) => {
        GridItem() {
          Column({ space: 6 }) {
            Text(item.label)
              .fontSize(13)
              .fontColor(item.key === this.current ? OgC.BRAND : OgC.INK)
              .fontWeight(item.key === this.current ? FontWeight.Bold : FontWeight.Normal)
          }
          .width('100%')
          .height(52)
          .justifyContent(FlexAlign.Center)
          .backgroundColor(item.key === this.current ? OgC.BRAND_LIGHT : OgC.CARD)
          .borderRadius(10)
          .opacity(this.locked(item) ? 0.35 : 1)
          .enabled(!this.locked(item))
          .onClick(() => this.go(item))
        }
      }, (item: NavItem) => item.key)
    }
    .columnsTemplate(this.bp === 'sm' ? '1fr 1fr 1fr' : '1fr 1fr 1fr 1fr')
    .columnsGap(8)
    .rowsGap(8)
    .width('100%')
    .height(Math.ceil(STAFF_NAV.length / this.cols()) * 62)
    .padding(OgDim.PAD)
  }
}
