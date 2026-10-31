// 考场编排(走班排考):生成 → 座位点选对调 → 监考芯片指派 → 冲突提示。
// 打印座位表留在桌面 Web;数据契约:GET/POST/PATCH /api/v1/exams/{id}/rooms。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { ExamDetail, ExamSummary, RoomsResp, Seat } from '../../model/Api';
import { OgSession } from '../../common/OgSession';
import { EmptyView, ErrorView, LoadingView, PrimaryButton, SectionHeader, TitleBar } from '../../components/Ui';

interface RoomsParams {
  exam: string;
}

interface SeatRef {
  room: string;
  idx: number;
}

interface SwapBody {
  a: SeatRef;
  b: SeatRef;
}

interface ProctorBody {
  room: string;
  teacher: string;
}

@Entry
@Component
struct RoomsPage {
  @State examId: string = '';
  @State d: ExamDetail | null = null;
  @State plan: RoomsResp | null = null;
  @State choices: ExamSummary[] = [];
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State busy: boolean = false;
  @State genOpen: boolean = false;
  @State genSize: string = '30';
  @State genGroup: string = 'class';
  @State selA: SeatRef | null = null; // 对调选择的第一座位

  aboutToAppear(): void {
    const p = router.getParams() as RoomsParams | null;
    if (p !== null && p.exam !== undefined) {
      this.examId = p.exam;
    }
    this.load();
  }

  private async load(): Promise<void> {
    this.loading = this.plan === null && this.choices.length === 0;
    this.failed = false;
    try {
      if (this.examId === '') {
        this.choices = await OgNet.jget<ExamSummary[]>('/api/v1/exams');
      } else {
        if (this.d === null) {
          this.d = await OgNet.jget<ExamDetail>('/api/v1/exams/' + this.examId);
        }
        this.plan = await OgNet.jget<RoomsResp>('/api/v1/exams/' + this.examId + '/rooms');
      }
    } catch (e) {
      if (e instanceof OgError && e.status === 404 && this.examId !== '') {
        this.plan = null; // 尚未生成编排
      } else {
        this.failed = true;
        this.failMsg = e instanceof OgError ? e.message : '加载失败';
      }
    }
    this.loading = false;
  }

  // 服务端 POST/PATCH /rooms = canEditExam(仅管理员/组长/建卷教师)
  private canEditRooms(): boolean {
    const me = OgSession.me();
    return me !== null && (me.role === 'admin' || me.role === 'leader'
      || (this.d !== null && me.username === this.d.createdBy));
  }

  private async generate(): Promise<void> {
    if (!this.canEditRooms()) {
      promptAction.showToast({ message: '仅管理员/年级组长/建卷教师可编排考场' });
      return;
    }
    const size = parseInt(this.genSize.trim(), 10);
    if (!(size >= 5 && size <= 200)) {
      promptAction.showToast({ message: '每场人数须为 5~200' });
      return;
    }
    this.busy = true;
    try {
      await OgNet.jpost<Record<string, Object>>('/api/v1/exams/' + this.examId + '/rooms', {
        'size': size, 'group': this.genGroup
      } as Record<string, Object>);
      promptAction.showToast({ message: '已生成' });
      this.genOpen = false;
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '生成失败', duration: 3200 });
    }
    this.busy = false;
  }

  private async swapSeats(a: SeatRef, b: SeatRef): Promise<void> {
    if (!this.canEditRooms()) {
      promptAction.showToast({ message: '仅管理员/年级组长/建卷教师可调整座位' });
      return;
    }
    this.busy = true;
    try {
      const swap: SwapBody = { a: a, b: b };
      await OgNet.jpatch<Record<string, Object>>('/api/v1/exams/' + this.examId + '/rooms', {
        'swap': swap
      } as Record<string, Object>);
      promptAction.showToast({ message: '已对调' });
      this.selA = null;
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '对调失败' });
      this.selA = null;
    }
    this.busy = false;
  }

  private async setProctor(room: string, teacher: string): Promise<void> {
    if (!this.canEditRooms()) {
      promptAction.showToast({ message: '仅管理员/年级组长/建卷教师可指派监考' });
      return;
    }
    this.busy = true;
    try {
      const proctor: ProctorBody = { room: room, teacher: teacher };
      await OgNet.jpatch<Record<string, Object>>('/api/v1/exams/' + this.examId + '/rooms', {
        'proctor': proctor
      } as Record<string, Object>);
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '指派失败' });
    }
    this.busy = false;
  }

  private staffNames(): string[] {
    if (this.d === null) {
      return [];
    }
    const out: string[] = [];
    for (const s of this.d.staff) {
      out.push(s.username);
    }
    return out;
  }

  private staffLabel(username: string): string {
    if (this.d === null) {
      return username;
    }
    for (const s of this.d.staff) {
      if (s.username === username) {
        return s.displayName;
      }
    }
    return username;
  }

  private seatAt(room: string, idx: number): Seat | null {
    if (this.plan === null) {
      return null;
    }
    for (const r of this.plan.rooms) {
      if (r.name === room) {
        for (const s of r.seats) {
          if (s.idx === idx) {
            return s;
          }
        }
      }
    }
    return null;
  }

  private isSelA(room: string, idx: number): boolean {
    return this.selA !== null && this.selA.room === room && this.selA.idx === idx;
  }

  private onSeatTap(room: string, idx: number): void {
    if (this.busy) {
      return;
    }
    const s = this.seatAt(room, idx);
    if (s === null || s.reg === '') {
      return; // 空位不参与对调
    }
    if (this.selA === null) {
      this.selA = { room: room, idx: idx } as SeatRef;
      return;
    }
    if (this.isSelA(room, idx)) {
      this.selA = null;
      return;
    }
    const a = this.selA;
    this.swapSeats(a, { room: room, idx: idx } as SeatRef);
  }

  @Builder
  choiceList() {
    Column({ space: OgDim.GAP }) {
      SectionHeader({ text: '选择要编排的考试' })
      if (this.choices.length === 0) {
        EmptyView({ text: '没有可见考试' })
      }
      ForEach(this.choices, (e: ExamSummary) => {
        Row({ space: 8 }) {
          Column({ space: 2 }) {
            Text((e.subject !== undefined && e.subject !== '') ? e.subject : '未标学科')
              .fontSize(14).fontWeight(FontWeight.Medium).fontColor(OgC.INK)
            Text(e.examDate !== undefined && e.examDate !== '' ? e.examDate : e.createdAt)
              .fontSize(11).fontColor(OgC.INK_SUB)
          }
          .alignItems(HorizontalAlign.Start)
          .layoutWeight(1)
          Text(e.stageLabel).fontSize(12).fontColor(OgC.BRAND)
          Text('›').fontSize(16).fontColor(OgC.INK_SUB)
        }
        .width('100%')
        .padding(12)
        .backgroundColor(OgC.CARD)
        .borderRadius(OgDim.RADIUS)
        .onClick(() => {
          this.examId = e.id;
          this.choices = [];
          this.load();
        })
      }, (e: ExamSummary) => e.id)
      Text('').height(16)
    }
    .padding(OgDim.PAD)
    .width('100%')
  }

  @Builder
  roomCard(room: RoomRow) {
    Column({ space: 8 }) {
      Row({ space: 8 }) {
        Text(room.name).fontSize(14).fontWeight(FontWeight.Bold).fontColor(OgC.INK).layoutWeight(1)
        Text(room.seats.length + ' 座').fontSize(11).fontColor(OgC.INK_SUB)
      }
      .width('100%')
      // 监考芯片
      Flex({ wrap: FlexWrap.Wrap }) {
        ForEach(this.staffNames(), (name: string) => {
          Text(this.staffLabel(name))
            .fontSize(11)
            .fontColor(room.teacher === name ? Color.White : OgC.INK)
            .backgroundColor(room.teacher === name ? OgC.BLUE : OgC.BG)
            .padding({ left: 8, right: 8, top: 4, bottom: 4 })
            .borderRadius(11)
            .margin({ right: 6, bottom: 4 })
            .onClick(() => {
              this.setProctor(room.name, room.teacher === name ? '' : name);
            })
        }, (name: string) => name + String(room.teacher === name))
        Text('不指派')
          .fontSize(11)
          .fontColor(room.teacher === '' || room.teacher === undefined ? Color.White : OgC.INK_SUB)
          .backgroundColor(room.teacher === '' || room.teacher === undefined ? OgC.INK_SUB : OgC.BG)
          .padding({ left: 8, right: 8, top: 4, bottom: 4 })
          .borderRadius(11)
          .margin({ right: 6, bottom: 4 })
          .onClick(() => this.setProctor(room.name, ''))
      }
      .width('100%')
      // 座位网格:点两个座位对调
      Grid() {
        ForEach(room.seats, (s: Seat) => {
          GridItem() {
            Column({ space: 1 }) {
              Text(s.reg === '' ? '空' : s.reg)
                .fontSize(9)
                .fontColor(s.reg === '' ? OgC.LINE : OgC.INK)
                .maxLines(1)
              Text(s.name !== undefined && s.name !== '' ? s.name : '·')
                .fontSize(9)
                .fontColor(OgC.INK_SUB)
                .maxLines(1)
                .textOverflow({ overflow: TextOverflow.Ellipsis })
            }
            .width('100%')
            .height(44)
            .justifyContent(FlexAlign.Center)
            .backgroundColor(this.isSelA(room.name, s.idx) ? OgC.BRAND : (s.reg === '' ? OgC.BG : OgC.CARD))
            .borderRadius(6)
            .border({ width: 1, color: this.isSelA(room.name, s.idx) ? OgC.BRAND : OgC.LINE })
            .onClick(() => this.onSeatTap(room.name, s.idx))
          }
        }, (s: Seat) => room.name + '-' + s.idx + '-' + s.reg + String(this.isSelA(room.name, s.idx)))
      }
      .columnsTemplate('1fr 1fr 1fr 1fr')
      .columnsGap(4)
      .rowsGap(4)
      .width('100%')
      .height(Math.ceil(room.seats.length / 4) * 50)
    }
    .width('100%')
    .padding(12)
    .backgroundColor(OgC.BG)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }

  build() {
    Column() {
      TitleBar({
        title: '考场编排',
        subtitle: this.examId === '' ? '选择考试' : this.examId,
        onBack: () => router.back()
      })
      if (this.loading) {
        LoadingView()
      } else if (this.failed) {
        ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
      } else if (this.examId === '') {
        Scroll() {
          this.choiceList()
        }
        .layoutWeight(1)
        .scrollBar(BarState.Off)
        .align(Alignment.Top)
      } else if (this.plan === null || this.plan.rooms.length === 0) {
        Column({ space: 14 }) {
          EmptyView({ text: this.canEditRooms() ? '尚未生成分场方案' : '尚未生成分场方案(仅建卷人/组长/管理员可编排)' })
          if (this.canEditRooms()) {
            PrimaryButton({
              full: true,
              label: '生成分场',
              onTap: () => {
                this.genOpen = true;
              }
            })
          }
        }
        .padding(OgDim.PAD)
        .width('100%')
      } else {
        Column({ space: 6 }) {
          Row({ space: 8 }) {
            SectionHeader({
              text: '共 ' + this.plan.rooms.length + ' 场',
              extra: '点两个座位对调;打印留电脑端'
            })
          }
          .width('100%')
          if (this.plan.conflicts !== undefined && this.plan.conflicts.length > 0) {
            Column({ space: 4 }) {
              ForEach(this.plan.conflicts, (c: ConflictRow) => {
                Text('⚠ 监考冲突:' + c.teacher + ' 同日同场次已在 '
                  + otherExamLabel(c) + ' ' + c.otherRoom + ' 监考')
                  .fontSize(11).fontColor(OgC.BAD)
              }, (c: ConflictRow, i: number) => 'c' + i)
            }
            .width('100%')
            .padding(8)
            .backgroundColor(OgC.BAD_BG)
            .borderRadius(8)
          }
          if (this.canEditRooms()) {
            PrimaryButton({
              full: true,
              label: '重新生成',
              onTap: () => {
                this.genOpen = true;
              }
            })
          }
        }
        .padding({ left: OgDim.PAD, right: OgDim.PAD, top: 8 })
        .width('100%')
        List({ space: 10 }) {
          ForEach(this.plan.rooms, (room: RoomRow) => {
            ListItem() {
              this.roomCard(room)
            }
          }, (room: RoomRow) => room.name + '-' + this.roomSig(room))
          ListItem() {
            Text('').height(20)
          }
        }
        .layoutWeight(1)
        .width('100%')
        .scrollBar(BarState.Off)
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
    .bindSheet($$this.genOpen, this.genBuilder(), {
      height: SheetSize.FIT_CONTENT, dragBar: true, title: { title: '生成分场方案' }
    })
  }

  private roomSig(room: RoomRow): string {
    let sig = '';
    for (const s of room.seats) {
      sig += s.reg + ',';
    }
    return sig + '|' + (room.teacher ?? '');
  }

  @Builder
  genBuilder() {
    Column({ space: 10 }) {
      Row({ space: 8 }) {
        Column({ space: 3 }) {
          Text('每场人数(5~200)').fontSize(11).fontColor(OgC.INK_SUB).width('100%')
          TextInput({ text: this.genSize, placeholder: '30' })
            .type(InputType.Number)
            .fontSize(14).height(40).width('100%')
            .onChange((v: string) => {
              this.genSize = v;
            })
        }
        .alignItems(HorizontalAlign.Start)
        .layoutWeight(1)
      }
      .width('100%')
      Row({ space: 6 }) {
        Text('同班聚齐')
          .fontSize(12)
          .fontColor(this.genGroup === 'class' ? Color.White : OgC.INK)
          .backgroundColor(this.genGroup === 'class' ? OgC.BRAND : OgC.BG)
          .padding({ left: 12, right: 12, top: 6, bottom: 6 })
          .borderRadius(12)
          .onClick(() => {
            this.genGroup = 'class';
          })
        Text('纯考号序')
          .fontSize(12)
          .fontColor(this.genGroup === 'reg' ? Color.White : OgC.INK)
          .backgroundColor(this.genGroup === 'reg' ? OgC.BRAND : OgC.BG)
          .padding({ left: 12, right: 12, top: 6, bottom: 6 })
          .borderRadius(12)
          .onClick(() => {
            this.genGroup = 'reg';
          })
      }
      .width('100%')
      PrimaryButton({
        full: true,
        usable: !this.busy,
        label: this.busy ? '生成中…' : '生成',
        onTap: () => this.generate()
      })
      Text('重新生成会覆盖现有编排(含对调与监考指派)。')
        .fontSize(11).fontColor(OgC.INK_SUB)
    }
    .padding(OgDim.PAD)
    .width('100%')
  }
}

interface RoomRow {
  name: string;
  teacher?: string;
  seats: Seat[];
}

interface ConflictRow {
  teacher: string;
  examId: string;
  subject?: string;
  date: string;
  session: string;
  room: string;
  otherRoom: string;
}

function otherExamLabel(c: ConflictRow): string {
  return c.subject !== undefined && c.subject !== '' ? c.subject : c.examId;
}
