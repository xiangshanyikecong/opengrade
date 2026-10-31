// 学生/家长端首页:底部三页签(成绩 / 趋势 / 我的)。
// 家长角色在顶部切换子女(?child= 全链路);列表只显示已发布的考试。

import { router } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
import { OgCache } from '../../common/OgCache';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { StudentExamItem } from '../../model/Api';
import { EmptyView, ErrorView, LoadingView, Pill } from '../../components/Ui';
import { StudentTrendsView } from './StudentTrendsPage';
import { MeView } from '../MePage';

@Entry
@Component
struct StudentHomePage {
  @State activeTab: number = 0;
  @State children: string[] = [];
  @State curChild: string = '';
  @State items: StudentExamItem[] = [];
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State refreshing: boolean = false;
  @State offline: boolean = false; // 断网时展示缓存数据
  @State newIds: Record<string, boolean> = {}; // 本机未读的新发布成绩

  aboutToAppear(): void {
    const me = OgSession.me();
    if (me !== null && me.role === 'parent' && me.children !== undefined) {
      this.children = me.children;
      if (this.children.length > 0) {
        this.curChild = this.children[0];
      }
    }
    this.load();
  }

  private childQ(): string {
    if (OgSession.me() !== null && OgSession.me()!.role === 'parent' && this.curChild !== '') {
      return '?child=' + this.curChild;
    }
    return '';
  }

  private cacheKey(): string {
    return 'student_exams_' + (this.curChild === '' ? 'self' : this.curChild);
  }

  private async seenMap(): Promise<Record<string, boolean>> {
    const raw = await OgSession.getJson('seenExams');
    if (raw === '') {
      return {};
    }
    try {
      return JSON.parse(raw) as Record<string, boolean>;
    } catch (e) {
      return {};
    }
  }

  private async markSeen(id: string): Promise<void> {
    const m = await this.seenMap();
    m[id] = true;
    await OgSession.setJson('seenExams', JSON.stringify(m));
    const next: Record<string, boolean> = {};
    for (const k of Object.keys(this.newIds)) {
      if (k !== id) {
        next[k] = true;
      }
    }
    this.newIds = next;
  }

  private async load(): Promise<void> {
    this.loading = this.items.length === 0;
    this.failed = false;
    this.offline = false;
    try {
      const resp = await OgNet.jget<Record<string, Object>>('/api/v1/student/exams' + this.childQ());
      const arr = resp['exams'] as StudentExamItem[];
      this.items = arr ?? [];
      OgCache.save(this.cacheKey(), JSON.stringify(resp));
      // 新发布角标:本机未见过的考试(仅首次浏览标记,进详情即消)
      const seen = await this.seenMap();
      const fresh: Record<string, boolean> = {};
      for (const it of this.items) {
        if (!it.absent && seen[it.id] !== true) {
          fresh[it.id] = true;
        }
      }
      this.newIds = fresh;
    } catch (e) {
      // 断网兜底:展示最近一次成功拉取的列表(带「离线数据」提示)
      const cached = OgCache.load(this.cacheKey());
      if (cached !== '') {
        try {
          const resp = JSON.parse(cached) as Record<string, Object>;
          this.items = (resp['exams'] as StudentExamItem[]) ?? [];
          this.offline = true;
        } catch (e2) {
          if (!this.refreshing) {
            this.failed = true;
            this.failMsg = e instanceof OgError ? e.message : '加载失败';
          }
        }
      } else if (!this.refreshing) {
        this.failed = true;
        this.failMsg = e instanceof OgError ? e.message : '加载失败';
      }
    }
    this.loading = false;
    this.refreshing = false;
  }

  @Builder
  examCard(it: StudentExamItem) {
    Column({ space: 6 }) {
      Row({ space: 8 }) {
        Text((it.subject !== undefined && it.subject !== '') ? it.subject : '未标学科')
          .fontSize(15).fontWeight(FontWeight.Bold).fontColor(OgC.INK).layoutWeight(1)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        if (this.newIds[it.id] === true) {
          Pill({ text: '新成绩', color: OgC.BAD })
        }
        if (it.absent) {
          Pill({ text: '缺考', color: OgC.WARN })
        }
        if (it.hasOpen === true) {
          Pill({ text: '复核处理中', color: OgC.WARN })
        }
        if (it.hasReply === true) {
          Pill({ text: '复核已回复', color: OgC.BLUE })
        }
      }
      .width('100%')
      if (!it.absent) {
        Row() {
          Text(OgFormat.score(it.score) + ' / ' + OgFormat.score(it.total))
            .fontSize(24)
            .fontWeight(FontWeight.Bold)
            .fontColor(OgC.BRAND)
          Blank()
          Column({ space: 2 }) {
            Text('年级排名 ' + it.rank).fontSize(12).fontColor(OgC.INK)
            if (it.classMean > 0) {
              Text('班级均分 ' + OgFormat.score(it.classMean)).fontSize(12).fontColor(OgC.INK_SUB)
            }
          }
          .alignItems(HorizontalAlign.End)
        }
        .width('100%')
      } else {
        Text('本场登记为缺考,不计入统计与排名').fontSize(12).fontColor(OgC.INK_SUB)
      }
      Text(it.examDate !== undefined && it.examDate !== '' ? it.examDate : it.createdAt)
        .fontSize(11).fontColor(OgC.INK_SUB)
    }
    .width('100%')
    .padding(14)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .onClick(() => {
      this.markSeen(it.id);
      router.pushUrl({
        url: 'pages/student/StudentExamDetailPage',
        params: { 'id': it.id, 'child': this.curChild } as Record<string, string>
      }).catch(() => {});
    })
  }

  @Builder
  examTab() {
    Refresh({ refreshing: this.refreshing }) {
      List({ space: OgDim.GAP }) {
        ListItem() {
          Column({ space: OgDim.GAP }) {
            if (this.loading) {
              LoadingView()
            } else if (this.failed) {
              ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
            } else if (this.items.length === 0) {
              EmptyView({ text: '还没有已发布的成绩' })
            }
            ForEach(this.items, (it: StudentExamItem) => {
              this.examCard(it)
            }, (it: StudentExamItem) => it.id + (it.child ?? ''))
          }
          .width('100%')
          .padding(OgDim.PAD)
        }
      }
      .width('100%')
      .height('100%')
      .scrollBar(BarState.Off)
    }
    .onRefreshing(() => {
      this.refreshing = true;
      this.load();
    })
    .width('100%')
    .height('100%')
  }

  @Builder
  tabBtn(idx: number, label: string) {
    Column({ space: 2 }) {
      Text(label)
        .fontSize(12)
        .fontColor(this.activeTab === idx ? OgC.BRAND : OgC.INK_SUB)
        .fontWeight(this.activeTab === idx ? FontWeight.Bold : FontWeight.Normal)
      Row().width(24).height(2).backgroundColor(this.activeTab === idx ? OgC.BRAND : Color.Transparent).borderRadius(1)
    }
    .layoutWeight(1)
    .height(48)
    .justifyContent(FlexAlign.Center)
    .onClick(() => {
      this.activeTab = idx;
    })
  }

  build() {
    Column() {
      // 家长多孩切换
      if (this.children.length > 1) {
        Row({ space: 8 }) {
          Text('子女:').fontSize(13).fontColor(OgC.INK_SUB)
          ForEach(this.children, (c: string) => {
            Text(c)
              .fontSize(12)
              .fontColor(this.curChild === c ? Color.White : OgC.BRAND)
              .backgroundColor(this.curChild === c ? OgC.BRAND : OgC.BRAND_LIGHT)
              .padding({ left: 10, right: 10, top: 4, bottom: 4 })
              .borderRadius(12)
              .onClick(() => {
                this.curChild = c;
                this.items = [];
                this.load();
              })
          }, (c: string) => c)
        }
        .width('100%')
        .padding({ left: OgDim.PAD, right: OgDim.PAD, top: 8, bottom: 4 })
      }
      if (this.activeTab === 0) {
        Column() {
          if (this.offline) {
            Text('⚠ 当前离线,展示最近一次加载的成绩(联网后自动刷新)')
              .fontSize(12).fontColor(OgC.WARN)
              .width('100%')
              .textAlign(TextAlign.Center)
              .padding(8)
              .backgroundColor(OgC.WARN_BG)
          }
          this.examTab()
        }
        .layoutWeight(1)
      } else if (this.activeTab === 1) {
        StudentTrendsView({ childOf: this.curChild })
      } else {
        MeView()
      }
      // 底部页签
      Row() {
        this.tabBtn(0, '成绩')
        this.tabBtn(1, '趋势')
        this.tabBtn(2, '我的')
      }
      .width('100%')
      .height(52)
      .backgroundColor(OgC.CARD)
      .border({ width: { top: 1 }, color: OgC.LINE })
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
  }
}
