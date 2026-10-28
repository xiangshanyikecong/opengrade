// 年级总分工表:同场分组多科聚合(各科得分 + 总分 + 年级/班级排名,缺考计 0)。
// 横向滚动表格;数据契约:GET /api/v1/aggregate?group=。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { AggregateExam, AggregateResp, AggregateStudent } from '../../model/Api';
import { EmptyView, ErrorView, LoadingView, PrimaryButton, SectionHeader, TitleBar } from '../../components/Ui';

const CELL_W: number = 64;

@Entry
@Component
struct AggregatePage {
  @State group: string = '';
  @State d: AggregateResp | null = null;
  @State loading: boolean = false;
  @State failed: boolean = false;
  @State failMsg: string = '';

  private async load(): Promise<void> {
    if (this.group.trim() === '') {
      promptAction.showToast({ message: '请输入同场分组标签(如 2026期中)' });
      return;
    }
    this.loading = true;
    this.failed = false;
    try {
      this.d = await OgNet.jget<AggregateResp>('/api/v1/aggregate' + OgNet.qs({ 'group': this.group.trim() }));
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  private exams(): AggregateExam[] {
    return this.d === null ? [] : this.d.exams;
  }

  private cellScore(st: AggregateStudent, ci: number): string {
    if (st.cells.length <= ci) {
      return '-';
    }
    const c = st.cells[ci];
    if (!c.found) {
      return '—';
    }
    if (c.absent === true) {
      return '缺';
    }
    return OgFormat.score(c.score);
  }

  build() {
    Column() {
      TitleBar({ title: '年级总表', showBack: false })
      Row({ space: 8 }) {
        TextInput({ placeholder: '同场分组,如 2026期中' })
          .fontSize(14).height(40).layoutWeight(1)
          .onChange((v: string) => {
            this.group = v;
          })
          .onSubmit(() => this.load())
        PrimaryButton({ label: '查询', onTap: () => this.load() })
      }
      .width('100%')
      .padding({ left: OgDim.PAD, right: OgDim.PAD, top: 8, bottom: 4 })

      if (this.loading) {
        LoadingView()
      } else if (this.failed) {
        ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
      } else if (this.d === null) {
        EmptyView({ text: '输入分组标签查询;多科考试需填同一「同场分组」才会聚合' })
      } else {
        Scroll() {
          Column({ space: OgDim.GAP }) {
            if (this.d.exams.length === 0) {
              EmptyView({ text: '该分组下没有可见考试' })
            } else {
              Column({ space: 6 }) {
                SectionHeader({
                  text: '分组 ' + this.d.group + ' · ' + this.d.exams.length + ' 科',
                  extra: this.d.totalExams !== this.d.exams.length
                    ? '总分仅按可见 ' + this.d.exams.length + '/' + this.d.totalExams + ' 科'
                    : ''
                })
              }
              .width('100%')
              .padding({ left: OgDim.PAD, right: OgDim.PAD })

              Scroll() {
                Column({ space: 0 }) {
                  // 表头
                  Row({ space: 0 }) {
                    this.headCell('班级', 64)
                    this.headCell('姓名', 72)
                    this.headCell('考号', 76)
                  ForEach(this.exams(), (e: AggregateExam) => {
                    this.headCell(e.subject !== undefined && e.subject !== '' ? e.subject : e.id, CELL_W)
                  }, (e: AggregateExam) => e.id)
                    this.headCell('总分', CELL_W)
                    this.headCell('年级', 52)
                    this.headCell('班', 44)
                  }
                  .border({ width: { bottom: 1 }, color: OgC.LINE })
                  ForEach(this.d.students, (st: AggregateStudent) => {
                    Row({ space: 0 }) {
                      this.bodyCell(st.class !== undefined ? st.class : '-', 64, OgC.INK_SUB)
                      this.bodyCell(st.name !== undefined ? st.name : '-', 72, OgC.INK)
                      this.bodyCell(st.reg, 76, OgC.INK_SUB)
                      ForEach(this.exams(), (e: AggregateExam, ci: number) => {
                        this.bodyCell(this.cellScore(st, ci), CELL_W, this.cellColor(st, ci))
                      }, (e: AggregateExam, ci: number) => 'c' + ci)
                      this.bodyCell(OgFormat.score(st.total), CELL_W, OgC.BRAND)
                      this.bodyCell(st.rank > 0 ? String(st.rank) : '-', 52, OgC.INK)
                      this.bodyCell(st.classRank !== undefined && st.classRank > 0 ? String(st.classRank) : '-',
                        44, OgC.INK_SUB)
                    }
                    .border({ width: { bottom: 1 }, color: OgC.LINE })
                  }, (st: AggregateStudent) => st.reg + '-' + st.total)
                }
              }
              .scrollable(ScrollDirection.Horizontal)
              .layoutWeight(1)
              .width('100%')
              .padding({ left: OgDim.PAD, right: OgDim.PAD, bottom: 20 })
            }
          }
          .width('100%')
        }
        .layoutWeight(1)
        .scrollBar(BarState.Off)
        .align(Alignment.Top)
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
  }

  private cellColor(st: AggregateStudent, ci: number): string {
    if (st.cells.length <= ci) {
      return OgC.INK_SUB;
    }
    const c = st.cells[ci];
    if (c.absent === true) {
      return OgC.WARN;
    }
    if (!c.found) {
      return OgC.LINE;
    }
    return OgC.INK;
  }

  @Builder
  headCell(label: string, w: number) {
    Text(label)
      .fontSize(11).fontColor(OgC.INK_SUB)
      .width(w).height(30)
      .textAlign(TextAlign.Center)
      .maxLines(1)
      .backgroundColor(OgC.BG)
  }

  @Builder
  bodyCell(label: string, w: number, color: string) {
    Text(label)
      .fontSize(12).fontColor(color)
      .width(w).height(32)
      .textAlign(TextAlign.Center)
      .maxLines(1)
      .textOverflow({ overflow: TextOverflow.Ellipsis })
  }
}
