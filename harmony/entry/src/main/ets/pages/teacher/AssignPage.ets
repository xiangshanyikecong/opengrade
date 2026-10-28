// 阅卷任务分配(管理员/年级组长):按题分配阅卷人 + 截止时间;按班分配模式。
// 保存:POST /exams/{id}/tasks {question, teacher} 或 {class, teacher};截止 POST /task-due {key, due}。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import { ExamDetail, StaffInfo } from '../../model/Api';
import { EmptyView, ErrorView, LoadingView, PrimaryButton, SectionHeader, TitleBar } from '../../components/Ui';

interface AssignParams {
  exam: string;
}

interface AssignRow {
  key: string; // q{n} 或班级名
  label: string;
  max: number;
  teacher: string;
  due: string;
}

@Entry
@Component
struct AssignPage {
  @State examId: string = '';
  @State d: ExamDetail | null = null;
  @State rows: AssignRow[] = [];
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State busy: boolean = false;

  aboutToAppear(): void {
    const p = router.getParams() as AssignParams | null;
    if (p !== null && p.exam !== undefined) {
      this.examId = p.exam;
    }
    this.load();
  }

  private async load(): Promise<void> {
    try {
      this.d = await OgNet.jget<ExamDetail>('/api/v1/exams/' + this.examId);
      const rows: AssignRow[] = [];
      if (this.d !== null) {
        if (this.d.taskMode === 'class') {
          for (const c of this.d.classes ?? []) {
            rows.push({
              key: c,
              label: c + ' 全科',
              max: 0,
              teacher: this.d.classTasks !== undefined ? (this.d.classTasks[c] ?? '') : '',
              due: this.d.taskDue !== undefined ? (this.d.taskDue[c] ?? '') : ''
            } as AssignRow);
          }
        } else {
          for (const p of this.d.progress) {
            rows.push({
              key: 'q' + p.question,
              label: '第 ' + (this.d.objective.length + p.question) + ' 题(满分 ' + OgFormat.score(p.max)
                + (p.elective !== undefined && p.elective !== '' ? ' · 选做' : '') + ')',
              max: p.max,
              teacher: p.teacher ?? '',
              due: this.d.taskDue !== undefined ? (this.d.taskDue['q' + p.question] ?? '') : ''
            } as AssignRow);
          }
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

  private async saveRow(row: AssignRow): Promise<void> {
    this.busy = true;
    try {
      if (this.d !== null && this.d.taskMode === 'class') {
        await OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/tasks', {
          'class': row.key, 'teacher': row.teacher
        } as Record<string, string>);
      } else {
        await OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/tasks', {
          'question': parseInt(row.key.substring(1), 10),
          'teacher': row.teacher
        } as Record<string, number | string>);
      }
      if (row.due.trim() !== '') {
        await OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/task-due', {
          'key': row.key, 'due': row.due.trim()
        } as Record<string, string>);
      }
      promptAction.showToast({ message: row.label + ' 已保存' });
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '保存失败', duration: 3000 });
    }
    this.busy = false;
  }

  private teacherNames(): string[] {
    if (this.d === null) {
      return [];
    }
    const out: string[] = [''];
    for (const s of this.d.staff) {
      out.push(s.username);
    }
    return out;
  }

  private teacherLabel(username: string): string {
    if (this.d === null) {
      return username;
    }
    for (const s of this.d.staff) {
      if (s.username === username) {
        return s.displayName + '(' + username + ')';
      }
    }
    return username;
  }

  build() {
    Column() {
      TitleBar({
        title: '任务分配',
        subtitle: this.examId + (this.d !== null && this.d.taskMode === 'class' ? ' · 按班模式' : ''),
        onBack: () => router.back()
      })
      if (this.loading) {
        LoadingView()
      } else if (this.failed) {
        ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
      } else if (this.rows.length === 0) {
        EmptyView({ text: '该考试没有可分配的任务(无主观题且非按班模式)' })
      } else {
        List({ space: 8 }) {
          ListItem() {
            Text('阅卷人下拉留空 = 取消分配;截止时间格式 YYYY-MM-DD。')
              .fontSize(11).fontColor(OgC.INK_SUB)
              .padding({ left: OgDim.PAD, right: OgDim.PAD, top: 8 })
          }
          ForEach(this.rows, (row: AssignRow) => {
            ListItem() {
              Column({ space: 8 }) {
                Row({ space: 8 }) {
                  Text(row.label).fontSize(13).fontWeight(FontWeight.Medium).fontColor(OgC.INK).layoutWeight(1)
                }
                .width('100%')
                Row({ space: 8 }) {
                  Select(this.teacherOptions(row.teacher))
                    .selected(this.teacherIdx(row.teacher))
                    .value(row.teacher === '' ? '选择阅卷人' : this.teacherLabel(row.teacher))
                    .fontColor(OgC.INK)
                    .font({ size: 13 })
                    .layoutWeight(1)
                    .height(38)
                    .onSelect((idx: number) => {
                      const names = this.teacherNames();
                      row.teacher = idx < names.length ? names[idx] : '';
                    })
                  TextInput({ text: row.due, placeholder: '截止' })
                    .fontSize(12).height(38).width(104)
                    .onChange((v: string) => {
                      row.due = v;
                    })
                  PrimaryButton({
                    label: this.busy ? '…' : '保存',
                    enabled: !this.busy,
                    onTap: () => this.saveRow(row)
                  })
                }
                .width('100%')
              }
              .padding(12)
              .margin({ left: OgDim.PAD, right: OgDim.PAD })
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)
              .width('100%')
            }
          }, (row: AssignRow) => row.key + '-' + row.teacher + '-' + row.due)
        }
        .layoutWeight(1)
        .width('100%')
        .scrollBar(BarState.Off)
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor(OgC.BG)
  }

  private teacherOptions(cur: string): SelectOption[] {
    const out: SelectOption[] = [];
    for (const n of this.teacherNames()) {
      out.push({ value: n === '' ? '(不分配)' : this.teacherLabel(n) } as SelectOption);
    }
    return out;
  }

  private teacherIdx(cur: string): number {
    return this.teacherNames().indexOf(cur);
  }
}
