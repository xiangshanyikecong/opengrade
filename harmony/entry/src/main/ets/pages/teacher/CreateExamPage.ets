// 建考建档(移动版):手工成绩科目直录建考 / 扫描考试建档(版式 JSON 经电脑设计器产出后导入)。
// 建考=建档:答案与扫描件都可后补,先建卡再拍照上传即可。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { picker } from '@kit.CoreFileKit';
import { OgNet, OgError, UploadFile } from '../../common/OgNet';
import { OgC, OgDim } from '../../common/OgTheme';
import { StudentRow } from '../../model/Api';
import { ErrorView, GhostButton, LoadingView, PrimaryButton, SectionHeader, TitleBar } from '../../components/Ui';

interface CreateExamResp {
  id: string;
}

interface CreateParams {
  presetLayout?: string; // 拍卷识别(LayoutScanPage)产出的版式 JSON,直接带入
  presetSubject?: string;
}

@Entry
@Component
struct CreateExamPage {
  @State manualMode: boolean = false;
  @State subject: string = '';
  @State manualFull: string = '';
  @State examDate: string = '';
  @State term: string = '';
  @State examType: string = '';
  @State examGroup: string = '';
  @State layoutText: string = '';
  @State keyText: string = '[]';
  @State abAuto: boolean = false;
  @State students: StudentRow[] = [];
  @State allClasses: string[] = [];
  @State pickedClasses: Record<string, boolean> = {};
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State busy: boolean = false;

  aboutToAppear(): void {
    const p = router.getParams() as CreateParams | null;
    if (p !== null && p !== undefined && p.presetLayout !== undefined && p.presetLayout !== '') {
      this.layoutText = p.presetLayout;
      if (p.presetSubject !== undefined && p.presetSubject !== '') {
        this.subject = p.presetSubject;
      }
      promptAction.showToast({ message: '已带入拍卷识别的版式,请核对分值后建考', duration: 3500 });
    }
    this.load();
  }

  private async load(): Promise<void> {
    try {
      this.students = (await OgNet.jget<StudentRow[]>('/api/v1/students')) ?? [];
      const set: Record<string, boolean> = {};
      for (const s of this.students) {
        for (const c of (s.classes ?? [])) {
          set[c] = true;
        }
      }
      const cls: string[] = [];
      for (const k of Object.keys(set)) {
        cls.push(k);
      }
      cls.sort();
      this.allClasses = cls;
      this.failed = false;
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '学生名单加载失败';
    }
    this.loading = false;
  }

  private pickedClassList(): string[] {
    const out: string[] = [];
    for (const c of this.allClasses) {
      if (this.pickedClasses[c] === true) {
        out.push(c);
      }
    }
    return out;
  }

  private rosterCount(): number {
    const picked = this.pickedClassList();
    let n = 0;
    for (const s of this.students) {
      for (const c of (s.classes ?? [])) {
        if (picked.indexOf(c) >= 0) {
          n++;
          break;
        }
      }
    }
    return n;
  }

  private rosterJson(): string {
    const picked = this.pickedClassList();
    const rows: Record<string, string>[] = [];
    for (const s of this.students) {
      for (const c of (s.classes ?? [])) {
        if (picked.indexOf(c) >= 0) {
          const row: Record<string, string> = { 'reg': s.reg, 'name': s.name, 'class': c };
          rows.push(row);
          break; // 主班口径(走班多归属时首班为主)
        }
      }
    }
    return JSON.stringify(rows);
  }

  private async pickLayoutFile(): Promise<void> {
    const dp = new picker.DocumentViewPicker();
    const opts: picker.DocumentSelectOptions = {
      maxSelectNumber: 1
    };
    try {
      const uris = await dp.select(opts);
      if (uris.length > 0) {
        this.layoutText = await OgNet.readUriText(uris[0]);
        promptAction.showToast({ message: '已载入版式 JSON(' + Math.round(this.layoutText.length / 1024) + 'KB)' });
      }
    } catch (e) {
      // 用户取消
    }
  }

  private async submit(): Promise<void> {
    const subject = this.subject.trim();
    if (subject === '') {
      promptAction.showToast({ message: '请填写学科' });
      return;
    }
    if (this.rosterCount() === 0) {
      promptAction.showToast({ message: '请至少勾选一个班级作为名册' });
      return;
    }
    this.busy = true;
    try {
      const fields: Record<string, string> = {
        'subject': subject,
        'roster': this.rosterJson(),
        'examDate': this.examDate.trim(),
        'term': this.term.trim(),
        'examType': this.examType.trim(),
        'examGroup': this.examGroup.trim()
      };
      if (this.manualMode) {
        const full = parseFloat(this.manualFull.trim());
        if (isNaN(full) || full <= 0) {
          promptAction.showToast({ message: '手工成绩考试请填写有效满分' });
          this.busy = false;
          return;
        }
        fields['manual'] = '1';
        fields['full'] = String(full);
      } else {
        if (this.layoutText.trim() === '') {
          promptAction.showToast({ message: '请导入版式 JSON(电脑端设计器导出)' });
          this.busy = false;
          return;
        }
        fields['layout'] = this.layoutText.trim();
        fields['key'] = this.keyText.trim() === '' ? '[]' : this.keyText.trim();
        if (this.abAuto) {
          fields['abAuto'] = '1';
        }
      }
      const files: UploadFile[] = [];
      const resp = await OgNet.upload('/api/v1/exams', fields, files);
      const m = JSON.parse(resp) as CreateExamResp;
      promptAction.showToast({ message: '建考成功:' + m.id });
      router.replaceUrl({
        url: 'pages/teacher/ExamHubPage', params: { 'exam': m.id } as Record<string, string>
      }).catch(() => {});
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '建考失败', duration: 4000 });
    }
    this.busy = false;
  }

  @Builder
  fieldRow(label: string, ph: string, bindTo: string) {
    Column({ space: 3 }) {
      Text(label).fontSize(11).fontColor(OgC.INK_SUB).width('100%')
      TextInput({ placeholder: ph })
        .fontSize(14).height(40).width('100%')
        .onChange((v: string) => {
          if (bindTo === 'subject') {
            this.subject = v;
          } else if (bindTo === 'full') {
            this.manualFull = v;
          } else if (bindTo === 'date') {
            this.examDate = v;
          } else if (bindTo === 'term') {
            this.term = v;
          } else if (bindTo === 'type') {
            this.examType = v;
          } else if (bindTo === 'group') {
            this.examGroup = v;
          }
        })
    }
    .alignItems(HorizontalAlign.Start)
    .layoutWeight(1)
  }

  build() {
    Column() {
      TitleBar({ title: '新建考试', onBack: () => router.back() })
      if (this.loading) {
        LoadingView()
      } else if (this.failed) {
        ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
      } else {
        Scroll() {
          Column({ space: OgDim.GAP }) {
            // 模式
            Row({ space: 12 }) {
              Text('手工成绩科目(体育/实验/听力,免答题卡)')
                .fontSize(13).fontColor(OgC.INK).layoutWeight(1)
              Toggle({ type: ToggleType.Switch, isOn: this.manualMode })
                .selectedColor(OgC.BRAND)
                .onChange((on: boolean) => {
                  this.manualMode = on;
                })
            }
            .width('100%')
            .padding(14)
            .backgroundColor(OgC.CARD)
            .borderRadius(OgDim.RADIUS)

            // 基本信息
            Column({ space: 10 }) {
              SectionHeader({ text: '基本信息' })
              Row({ space: 10 }) {
                this.fieldRow('学科 *', '如 数学', 'subject')
                if (this.manualMode) {
                  this.fieldRow('满分 *', '如 100', 'full')
                }
              }
              Row({ space: 10 }) {
                this.fieldRow('考试日期', '2026-10-24', 'date')
                this.fieldRow('学期', '2026秋季', 'term')
              }
              Row({ space: 10 }) {
                this.fieldRow('类型', '期中', 'type')
                this.fieldRow('同场分组', '2026期中', 'group')
              }
            }
            .width('100%')
            .padding(14)
            .backgroundColor(OgC.CARD)
            .borderRadius(OgDim.RADIUS)
            .alignItems(HorizontalAlign.Start)

            // 名册
            Column({ space: 8 }) {
              SectionHeader({ text: '名册(勾选班级自动带出)', extra: this.rosterCount() + ' 人' })
              Flex({ wrap: FlexWrap.Wrap }) {
                ForEach(this.allClasses, (c: string) => {
                  Text((this.pickedClasses[c] === true ? '✓ ' : '') + c)
                    .fontSize(13)
                    .fontColor(this.pickedClasses[c] === true ? Color.White : OgC.INK)
                    .backgroundColor(this.pickedClasses[c] === true ? OgC.BRAND : OgC.BG)
                    .border({ width: 1, color: this.pickedClasses[c] === true ? OgC.BRAND : OgC.LINE, radius: 14 })
                    .padding({ left: 12, right: 12, top: 6, bottom: 6 })
                    .margin({ right: 8, bottom: 8 })
                    .onClick(() => {
                      const cur = this.pickedClasses[c] === true;
                      const next: Record<string, boolean> = {};
                      for (const k of Object.keys(this.pickedClasses)) {
                        next[k] = this.pickedClasses[k];
                      }
                      next[c] = !cur;
                      this.pickedClasses = next;
                    })
                }, (c: string) => c + String(this.pickedClasses[c] === true))
              }
              .width('100%')
            }
            .width('100%')
            .padding(14)
            .backgroundColor(OgC.CARD)
            .borderRadius(OgDim.RADIUS)
            .alignItems(HorizontalAlign.Start)

            // 版式(仅常规)
            if (!this.manualMode) {
              Column({ space: 8 }) {
                SectionHeader({ text: '答题卡版式(JSON)', extra: '识别 / 设计器' })
                Text('可「拍试卷识别」自动生成版式,或电脑端设计器(index.html)制作后经「文件」导入;扫描件可建考后在手机拍照上传。')
                  .fontSize(11).fontColor(OgC.INK_SUB)
                Row({ space: 10 }) {
                  PrimaryButton({ label: '📷 拍试卷识别', onTap: () => {
                    router.pushUrl({ url: 'pages/teacher/LayoutScanPage' }).catch(() => {});
                  } })
                  PrimaryButton({ label: '📂 导入版式文件', onTap: () => this.pickLayoutFile() })
                  GhostButton({ label: '清空', onTap: () => {
                    this.layoutText = '';
                  } })
                }
                .width('100%')
                if (this.layoutText !== '') {
                  Text('已载入 ' + Math.round(this.layoutText.length / 1024) + 'KB')
                    .fontSize(12).fontColor(OgC.GOOD)
                }
                Column({ space: 3 }) {
                  Text('参考答案(JSON 数组,可后补;留 [] 占位)')
                    .fontSize(11).fontColor(OgC.INK_SUB).width('100%')
                  TextArea({ text: this.keyText, placeholder: '如 ["A","T","AC"] 或 []' })
                    .fontSize(12).height(70)
                    .onChange((v: string) => {
                      this.keyText = v;
                    })
                }
                .width('100%')
                .alignItems(HorizontalAlign.Start)
                Row({ space: 8 }) {
                  Text('启用 AB 卷(自动派生 B 卷)').fontSize(13).fontColor(OgC.INK)
                  Blank()
                  Toggle({ type: ToggleType.Switch, isOn: this.abAuto })
                    .selectedColor(OgC.BRAND)
                    .onChange((on: boolean) => {
                      this.abAuto = on;
                    })
                }
                .width('100%')
              }
              .width('100%')
              .padding(14)
              .backgroundColor(OgC.CARD)
              .borderRadius(OgDim.RADIUS)
              .alignItems(HorizontalAlign.Start)
            }

            PrimaryButton({
              full: true,
              label: this.busy ? '建考中…' : '建考(建档)',
              usable: !this.busy,
              onTap: () => this.submit()
            })
            Text('').height(24)
          }
          .padding(OgDim.PAD)
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
}
