// 成绩直录:手工成绩科目逐格直录(手工考试)/ 缺考登记(扫描考试漏扫兜底)。
// 手工考试:POST /manual-scores {scores:[{reg, score?, absent?}]};统计与扫描科目同口径。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { ExamDetail, RosterRow } from '../../model/Api';
import { ErrorView, LoadingView, PrimaryButton, SectionHeader, TitleBar } from '../../components/Ui';

interface ScoresParams {
  exam: string;
  entryReg: string;
}

interface ScoreRow {
  reg: string;
  name: string;
  classes: string;
  buf: string; // 输入缓冲
  absent: boolean;
}

@Entry
@Component
struct ManualScoresPage {
  @State examId: string = '';
  @State d: ExamDetail | null = null;
  @State rows: ScoreRow[] = [];
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State busy: boolean = false;

  aboutToAppear(): void {
    const p = router.getParams() as ScoresParams | null;
    if (p !== null && p.exam !== undefined) {
      this.examId = p.exam;
    }
    this.load();
  }

  private async load(): Promise<void> {
    try {
      this.d = await OgNet.jget<ExamDetail>('/api/v1/exams/' + this.examId);
      const rows: ScoreRow[] = [];
      if (this.d !== null) {
        const absentRegs: Record<string, boolean> = {};
        for (const a of this.d.absent) {
          absentRegs[a.reg] = true;
        }
        for (const r of this.d.roster) {
          const existing = this.d.manualScores !== undefined ? this.d.manualScores[r.reg] : undefined;
          rows.push({
            reg: r.reg,
            name: r.name ?? '',
            classes: r.classes !== undefined ? r.classes.join('/') : '',
            buf: existing !== undefined ? OgFormat.score(existing) : '',
            absent: absentRegs[r.reg] === true
          } as ScoreRow);
        }
      }
      this.rows = rows;
      this.failed = false;
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  private async saveAll(): Promise<void> {
    const scores: Record<string, Object>[] = [];
    for (const r of this.rows) {
      const item: Record<string, Object> = { 'reg': r.reg };
      if (r.absent) {
        item['absent'] = true;
      } else if (r.buf.trim() !== '') {
        const v = parseFloat(r.buf.trim());
        if (isNaN(v)) {
          promptAction.showToast({ message: r.reg + ' 的分数不是合法数值:' + r.buf });
          return;
        }
        item['score'] = v;
      }
      scores.push(item);
    }
    this.busy = true;
    try {
      await OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/manual-scores', {
        'scores': scores
      } as Record<string, Object>);
      promptAction.showToast({ message: '已保存 ' + scores.length + ' 条' });
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '保存失败', duration: 3000 });
    }
    this.busy = false;
  }

  private toggleAbsent(row: ScoreRow): void {
    row.absent = !row.absent;
    if (row.absent) {
      row.buf = '';
    }
    this.rows = this.rows.slice(0); // 触发重渲染
  }

  private regCount(): number {
    let n = 0;
    for (const r of this.rows) {
      if (r.buf.trim() !== '' || r.absent) {
        n++;
      }
    }
    return n;
  }

  build() {
    Column() {
      TitleBar({
        title: this.d !== null && this.d.manualMode === true ? '成绩直录' : '漏扫补录 / 缺考',
        subtitle: this.examId,
        onBack: () => router.back()
      })
      if (this.loading) {
        LoadingView()
      } else if (this.failed) {
        ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
      } else {
        Column({ space: 8 }) {
          if (this.d !== null && this.d.manualMode !== true) {
            Text('本考试为扫描考试:此处可为漏扫/答题卡作废的学生登记缺考;手输客观题作答的完整补录请在电脑端操作。')
              .fontSize(12).fontColor(OgC.WARN)
              .padding(10)
              .backgroundColor(OgC.WARN_BG)
              .borderRadius(8)
              .width('100%')
          }
          SectionHeader({
            text: '名册(' + this.rows.length + ' 人)',
            extra: '已填 ' + this.regCount() + ' · 满分 ' + (this.d !== null ? OgFormat.score(this.d.manualFull ?? 0) : '-')
          })
        }
        .width('100%')
        .padding({ left: OgDim.PAD, right: OgDim.PAD, top: 8 })
        List({ space: 4 }) {
          ForEach(this.rows, (r: ScoreRow) => {
            ListItem() {
              Row({ space: 8 }) {
                Column({ space: 1 }) {
                  Text((r.name !== '' ? r.name + ' ' : '') + r.reg)
                    .fontSize(13).fontColor(r.absent ? OgC.INK_SUB : OgC.INK)
                    .decoration({ type: TextDecorationType.LineThrough, color: OgC.INK_SUB })
                  if (r.classes !== '') {
                    Text(r.classes).fontSize(10).fontColor(OgC.INK_SUB)
                  }
                }
                .alignItems(HorizontalAlign.Start)
                .layoutWeight(1)
                if (this.d !== null && this.d.manualMode === true) {
                  TextInput({ text: r.buf, placeholder: '分数' })
                    .type(InputType.NUMBER_DECIMAL)
                    .fontSize(14).height(36).width(96)
                    .enabled(!r.absent)
                    .onChange((v: string) => {
                      r.buf = v;
                    })
                }
                Text(r.absent ? '缺考 ✓' : '标缺考')
                  .fontSize(12)
                  .fontColor(r.absent ? OgC.BAD : OgC.INK_SUB)
                  .padding({ left: 10, right: 10, top: 6, bottom: 6 })
                  .borderRadius(10)
                  .backgroundColor(r.absent ? OgC.BAD_BG : OgC.BG)
                  .onClick(() => this.toggleAbsent(r))
              }
              .width('100%')
              .padding({ left: OgDim.PAD, right: OgDim.PAD, top: 5, bottom: 5 })
            }
          }, (r: ScoreRow) => r.reg + '-' + r.buf + '-' + String(r.absent))
        }
        .layoutWeight(1)
        .width('100%')
        .scrollBar(BarState.Off)

        Row({ space: 10 }) {
          PrimaryButton({
            label: this.busy ? '保存中…' : '保存全部(' + this.regCount() + ')',
            usable: !this.busy,
            onTap: () => this.saveAll()
          })
        }
        .padding({ left: OgDim.PAD, right: OgDim.PAD, bottom: 12 })
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
  }
}
