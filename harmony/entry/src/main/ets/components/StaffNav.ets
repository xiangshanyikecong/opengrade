// 教职员侧栏导航(☰ 弹出):所有工作台页共用,点击跳转对应页。
// current 标识当前页(置灰不跳);adminOnly 项仅管理员可见。

import { router } from '@kit.ArkUI';
import { OgSession } from '../common/OgSession';
import { OgC, OgDim } from '../common/OgTheme';

export interface NavItem {
  key: string;
  label: string;
  url: string;
  adminOnly: boolean;
}

export const STAFF_NAV: NavItem[] = [
  { key: 'tasks', label: '我的任务', url: 'pages/teacher/TaskListPage', adminOnly: false },
  { key: 'exams', label: '考试列表', url: 'pages/teacher/ExamListPage', adminOnly: false },
  { key: 'create', label: '新建考试', url: 'pages/teacher/CreateExamPage', adminOnly: false },
  { key: 'assign', label: '任务分配', url: 'pages/teacher/AssignPage', adminOnly: false },
  { key: 'trends', label: '纵向趋势', url: 'pages/teacher/StaffTrendsPage', adminOnly: false },
  { key: 'aggregate', label: '年级总表', url: 'pages/teacher/AggregatePage', adminOnly: false },
  { key: 'bank', label: '校本题库', url: 'pages/teacher/BankListPage', adminOnly: false },
  { key: 'users', label: '用户管理', url: 'pages/admin/UserListPage', adminOnly: true },
  { key: 'me', label: '账号设置', url: 'pages/MePage', adminOnly: false }
];

@Component
export struct StaffNavGrid {
  current: string = '';
  onClose: () => void = () => {};

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
          .opacity(item.adminOnly && !OgSession.isAdmin() ? 0.35 : 1)
          .enabled(!(item.adminOnly && !OgSession.isAdmin()))
          .onClick(() => this.go(item))
        }
      }, (item: NavItem) => item.key)
    }
    .columnsTemplate('1fr 1fr 1fr')
    .columnsGap(8)
    .rowsGap(8)
    .width('100%')
    .height(Math.ceil(STAFF_NAV.length / 3) * 62)
    .padding(OgDim.PAD)
  }
}
