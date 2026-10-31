// 考试详情枢纽:阶段统计、逐题进度、异常清单逐条裁决、重复卷、缺扫名单、
// 已作废卷、⚑待复核、复核申请裁决、补传扫描、手工成绩、发布/撤回、元数据、
// 参考答案修正、导出下载、删除。是教师手机端的"考试控制台"。
// 弹层统一走单个 bindSheet(按 sheetKind 切内容:nav/assign/meta/key/reply)。

import { router } from '@kit.ArkUI';
import { promptAction } from '@kit.ArkUI';
import { OgNet, OgError } from '../../common/OgNet';
import { OgSession } from '../../common/OgSession';
import { OgC, OgDim } from '../../common/OgTheme';
import { OgFormat } from '../../common/OgFormat';
import {
  AppealView, AuditEntry, ExamDetail, RosterCandidate, RosterRow, SheetWarning, SuggestResp
} from '../../model/Api';
import {
  EmptyView, ErrorView, LoadingView, Pill, PrimaryButton, GhostButton, SectionHeader, TitleBar
} from '../../components/Ui';
import { NAV_SHEET_HEIGHT, StaffNavGrid } from '../../components/StaffNav';

interface HubParams {
  exam: string;
}

@Entry
@Component
struct ExamHubPage {
  @State examId: string = '';
  @State d: ExamDetail | null = null;
  @State loading: boolean = true;
  @State failed: boolean = false;
  @State failMsg: string = '';
  @State sheetKind: string = ''; // '' | nav | assign | meta | key | reply
  @State sheetOpen: boolean = false;
  // 推荐考号弹层
  @State assignStudent: number = 0;
  @State suggestions: RosterCandidate[] = [];
  @State customReg: string = '';
  // 元数据编辑
  @State mExamDate: string = '';
  @State mTerm: string = '';
  @State mExamType: string = '';
  @State mExamGroup: string = '';
  @State mAnonymous: boolean = false;
  // 参考答案修正
  @State keyBuf: string = '';
  // 复核回复
  @State appealReply: string = '';
  @State appealNo: number = 0;
  // 审计日志
  @State auditOpen: boolean = false;
  @State audit: AuditEntry[] = [];
  @State auditLoading: boolean = false;

  aboutToAppear(): void {
    const p = router.getParams() as HubParams | null;
    if (p !== null && p !== undefined && p.exam !== undefined) {
      this.examId = p.exam;
    }
    this.load();
  }

  private async load(): Promise<void> {
    this.loading = this.d === null;
    this.failed = false;
    try {
      this.d = await OgNet.jget<ExamDetail>('/api/v1/exams/' + this.examId);
    } catch (e) {
      this.failed = true;
      this.failMsg = e instanceof OgError ? e.message : '加载失败';
    }
    this.loading = false;
  }

  private async act(desc: string, fn: () => Promise<Object>): Promise<void> {
    try {
      await fn();
      promptAction.showToast({ message: desc + '完成' });
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: (e instanceof OgError ? e.message : '操作失败'), duration: 3000 });
    }
  }

  private canEdit(): boolean {
    const me = OgSession.me();
    return this.d !== null && me !== null &&
      (me.role === 'admin' || me.role === 'leader' || me.username === this.d.createdBy);
  }

  private openSheet(kind: string): void {
    this.sheetKind = kind;
    this.sheetOpen = true;
  }

  private closeSheet(): void {
    this.sheetOpen = false;
    this.sheetKind = '';
  }

  private async openSuggest(student: number): Promise<void> {
    this.assignStudent = student;
    this.suggestions = [];
    this.customReg = '';
    this.openSheet('assign');
    try {
      const resp = await OgNet.jget<SuggestResp>('/api/v1/exams/' + this.examId + '/suggest/' + student);
      this.suggestions = resp.suggestions ?? [];
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '推荐获取失败' });
    }
  }

  private confirmAssign(): void {
    const reg = this.customReg.trim();
    if (reg === '') {
      promptAction.showToast({ message: '请选择推荐考号或输入考号' });
      return;
    }
    const student = this.assignStudent;
    this.closeSheet();
    this.act('考号指派', (): Promise<Object> => {
      return OgNet.jpost<Record<string, string>>('/api/v1/assign', {
        'examId': this.examId, 'student': student, 'reg': reg
      } as Record<string, Object>);
    });
  }

  private missingRows(): RosterRow[] {
    if (this.d === null) {
      return [];
    }
    const absentRegs: Record<string, boolean> = {};
    for (const a of (this.d.absent ?? [])) {
      absentRegs[a.reg] = true;
    }
    const out: RosterRow[] = [];
    const scanned: Record<string, boolean> = this.d.scannedReg ?? {};
    for (const r of (this.d.roster ?? [])) {
      if (r.reg !== '' && scanned[r.reg] !== true && !absentRegs[r.reg]) {
        out.push(r);
      }
    }
    return out;
  }

  private async togglePublish(): Promise<void> {
    if (this.d === null) {
      return;
    }
    const target = !this.d.published;
    await this.act(target ? '发布' : '撤回发布', (): Promise<Object> => {
      return OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/publish', {
        'published': target
      } as Record<string, Object>);
    });
  }

  private async assignVariant(student: number, variant: string): Promise<void> {
    await this.act('卷种指派', (): Promise<Object> => {
      return OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/assign-variant', {
        'student': student, 'variant': variant
      } as Record<string, Object>);
    });
  }

  private async keepSheet(group: number[], keep: number): Promise<void> {
    try {
      for (const s of group) {
        if (s === keep) {
          continue;
        }
        await OgNet.jpost<Record<string, string>>(
          '/api/v1/exams/' + this.examId + '/results/' + s + '/void', {
            'voided': true
          } as Record<string, Object>);
      }
      promptAction.showToast({ message: '已保留卷 ' + keep + ',组内其余已作废' });
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '裁决失败' });
    }
  }

  private regOfSheet(sheet: number): string {
    if (this.d === null || this.d.regOf === undefined) {
      return '?';
    }
    return this.d.regOf[String(sheet)] ?? '?';
  }

  private async deleteExam(): Promise<void> {
    await this.act('删除', (): Promise<Object> => {
      return OgNet.jdel<Record<string, string>>('/api/v1/exams/' + this.examId);
    });
    router.back();
  }

  private async download(it: ExportItem): Promise<void> {
    try {
      promptAction.showToast({ message: '正在生成 ' + it.label + '…', duration: 2500 });
      const uri = await OgNet.downloadToPicker('/api/v1/exams/' + this.examId + it.path, it.name);
      if (uri !== '') {
        promptAction.showToast({ message: '已保存:' + it.name });
      }
    } catch (e) {
      promptAction.showToast({ message: e instanceof OgError ? e.message : '下载失败', duration: 3000 });
    }
  }

  private exportItems(): ExportItem[] {
    return [
      { label: '成绩册 xlsx', path: '/export.xlsx', name: 'opengrade-' + this.examId + '.xlsx' },
      { label: '成绩册 csv', path: '/export.csv', name: 'opengrade-' + this.examId + '.csv' },
      { label: '全量数据包 zip', path: '/export.zip', name: 'opengrade-' + this.examId + '-full.zip' },
      { label: '讲评课件 pptx', path: '/review.pptx', name: 'opengrade-' + this.examId + '-review.pptx' },
      { label: '成绩条 PDF', path: '/slips.pdf', name: 'opengrade-slips-' + this.examId + '.pdf' }
    ];
  }

  // —— UI ——

  @Builder
  navAction() {
    Text('☰').fontSize(20).fontColor(OgC.BRAND).padding({ left: 10, right: 4 })
      .onClick(() => {
        this.openSheet('nav');
      })
  }

  @Builder
  stageCard() {
    Column({ space: 8 }) {
      Row({ space: 8 }) {
        Text(this.d !== null ? this.d.stage.label : '')
          .fontSize(16).fontWeight(FontWeight.Bold).fontColor(OgC.BRAND)
        Blank()
        if (this.d !== null && this.d.published) {
          Pill({ text: '已发布', color: OgC.GOOD })
          if (this.d.stage.dirty) {
            Pill({ text: '有变更待重发', color: OgC.WARN })
          }
        }
      }
      .width('100%')
      Row({ space: 10 }) {
        Text('已扫 ' + (this.d !== null ? this.d.stage.students : 0)
          + (this.d !== null && this.d.stage.expected > 0 ? '/' + this.d.stage.expected : ''))
          .fontSize(12).fontColor(OgC.INK_SUB)
        Text('异常 ' + (this.d !== null ? this.d.stage.warnings : 0)).fontSize(12).fontColor(OgC.BAD)
        Text('未阅 ' + (this.d !== null ? this.d.stage.ungraded : 0)).fontSize(12).fontColor(OgC.WARN)
        Text('缺考 ' + (this.d !== null ? this.d.stage.absent : 0)).fontSize(12).fontColor(OgC.INK_SUB)
        Blank()
      }
      .width('100%')
      Progress({
        value: this.d !== null ? this.d.stage.students : 0,
        total: this.d !== null && this.d.stage.expected > 0 ? this.d.stage.expected : 1,
        type: ProgressType.Linear
      })
        .color(OgC.BRAND).height(8).width('100%')
      Row({ space: 8 }) {
        PrimaryButton({
          label: '补传扫描',
          fill: true,
          onTap: () => {
            router.pushUrl({
              url: 'pages/teacher/UploadScansPage', params: { 'exam': this.examId } as Record<string, Object>
            }).catch(() => {});
          }
        })
        if (this.d !== null && this.d.manualMode === true) {
          PrimaryButton({
            label: '录成绩',
            fill: true,
            onTap: () => {
              router.pushUrl({
                url: 'pages/teacher/ManualScoresPage', params: { 'exam': this.examId } as Record<string, Object>
              }).catch(() => {});
            }
          })
        }
        GhostButton({
          label: '学情',
          onTap: () => {
            router.pushUrl({
              url: 'pages/teacher/AnalyticsPage', params: { 'exam': this.examId } as Record<string, Object>
            }).catch(() => {});
          }
        })
        if (this.canEdit()) {
          GhostButton({
            label: this.d !== null && this.d.published ? '撤回发布' : '发布成绩',
            onTap: () => this.togglePublish()
          })
        }
      }
      .width('100%')
    }
    .width('100%')
    .padding(14)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }


  // 卡片壳:统一在页面内调用自身 @Builder 方法(不跨组件传 Builder 引用,避免 this 丢失闪退)
  @Builder
  progressCard() {
    Column({ space: 6 }) {
      SectionHeader({ text: '阅卷进度(逐题)', extra: this.d !== null ? (this.d.progress ?? []).length + ' 道主观题' : '' })
      this.progressList()
    }
    .width('100%')
    .padding(14)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }

  @Builder
  warningCard() {
    Column({ space: 6 }) {
      SectionHeader({ text: '异常清单', extra: this.d !== null ? this.d.stage.warnings + ' 待裁决' : '' })
      this.warningList()
    }
    .width('100%')
    .padding(14)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }

  @Builder
  dupCard() {
    Column({ space: 6 }) {
      SectionHeader({ text: '重复卷(同考号多份)', extra: '保留一份' })
      this.dupList()
    }
    .width('100%')
    .padding(14)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }

  @Builder
  missingCard() {
    Column({ space: 6 }) {
      SectionHeader({ text: '缺扫名单', extra: this.missingRows().length + ' 人' })
      this.missingList()
    }
    .width('100%')
    .padding(14)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }

  @Builder
  voidedCard() {
    Column({ space: 6 }) {
      SectionHeader({ text: '已作废卷', extra: '可恢复' })
      this.voidedList()
    }
    .width('100%')
    .padding(14)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }

  @Builder
  flaggedCard() {
    Column({ space: 6 }) {
      SectionHeader({ text: '⚑ 待复核队列', extra: this.d !== null ? (this.d.flagged ?? []).length + ' 份' : '' })
      this.flaggedList()
    }
    .width('100%')
    .padding(14)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }

  @Builder
  appealCard() {
    Column({ space: 6 }) {
      SectionHeader({ text: '成绩复核申请', extra: '' })
      this.appealList()
    }
    .width('100%')
    .padding(14)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }

  @Builder
  metaCard() {
    Column({ space: 6 }) {
      SectionHeader({ text: '考试信息', extra: '' })
      this.metaList()
    }
    .width('100%')
    .padding(14)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }

  @Builder
  exportCard() {
    Column({ space: 6 }) {
      SectionHeader({ text: '导出与打印', extra: '保存到「文件」' })
      this.exportList()
    }
    .width('100%')
    .padding(14)
    .backgroundColor(OgC.CARD)
    .borderRadius(OgDim.RADIUS)
    .alignItems(HorizontalAlign.Start)
  }

  @Builder
  progressList() {
    ForEach(this.d !== null ? (this.d.progress ?? []) : [], (p: ProgressRow) => {
      Row({ space: 8 }) {
        Column({ space: 2 }) {
          Text('第 ' + (this.d !== null ? (this.d.objective ?? []).length + p.question : p.question) + ' 题 · 满分 '
            + OgFormat.score(p.max) + (p.elective !== undefined && p.elective !== '' ? ' · 选做' : ''))
            .fontSize(13).fontColor(OgC.INK)
          Text((p.teacher !== undefined && p.teacher !== '' ? '阅卷人 ' + p.teacher + ' · ' : '')
            + p.graded + '/' + p.total).fontSize(11).fontColor(OgC.INK_SUB)
        }
        .alignItems(HorizontalAlign.Start)
        .layoutWeight(1)
        Text('阅本题 ›').fontSize(12).fontColor(OgC.BRAND)
          .onClick(() => {
            router.pushUrl({
              url: 'pages/teacher/GraderPage',
              params: { 'exam': this.examId, 'q': String(p.question) } as Record<string, Object>
            }).catch(() => {});
          })
      }
      .width('100%')
      .padding({ top: 6, bottom: 6 })
      .border({ width: { bottom: 1 }, color: OgC.LINE })
    }, (p: ProgressRow) => 'q' + p.question + '-' + p.graded)
    if (this.d !== null && this.d.progress.length === 0) {
      Text('无主观题(纯客观卷)').fontSize(12).fontColor(OgC.INK_SUB)
    }
  }

  @Builder
  warningList() {
    ForEach(this.d !== null ? (this.d.warnings ?? []) : [], (w: SheetWarning) => {
      Column({ space: 6 }) {
        Row({ space: 8 }) {
          Text('卷 ' + w.student + (w.reg !== undefined && w.reg !== '' ? ' · ' + w.reg : ''))
            .fontSize(13).fontWeight(FontWeight.Medium).fontColor(OgC.INK)
          if (w.variant !== undefined && w.variant !== '') {
            Pill({ text: w.variant + ' 卷', color: OgC.BLUE })
          }
          Blank()
          if (w.soft) {
            Pill({ text: '提示类', color: OgC.INK_SUB })
          } else if (w.resolved) {
            Pill({ text: '已裁决', color: OgC.GOOD })
          } else {
            Pill({ text: '待裁决', color: OgC.BAD })
          }
        }
        .width('100%')
        ForEach(w.texts, (t: string) => {
          Text('· ' + t).fontSize(11).fontColor(OgC.INK_SUB)
        }, (t: string) => t)
        if (!w.resolved) {
          Row({ space: 10 }) {
            Text('推荐考号…').fontSize(12).fontColor(OgC.BRAND)
              .onClick(() => {
                this.openSuggest(w.student);
              })
            Text('标缺考').fontSize(12).fontColor(OgC.WARN)
              .onClick(() => {
                this.act('缺考登记', (): Promise<Object> => {
                  return OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/absent', {
                    'student': w.student, 'absent': true
                  } as Record<string, Object>);
                });
              })
            Text('作废').fontSize(12).fontColor(OgC.BAD)
              .onClick(() => {
                this.act('作废', (): Promise<Object> => {
                  return OgNet.jpost<Record<string, string>>(
                    '/api/v1/exams/' + this.examId + '/results/' + w.student + '/void', {
                      'voided': true
                    } as Record<string, Object>);
                });
              })
            if (this.d !== null && this.d.hasB === true && this.canEdit()) {
              Text('指派A').fontSize(12).fontColor(OgC.BLUE)
                .onClick(() => this.assignVariant(w.student, 'A'))
              Text('指派B').fontSize(12).fontColor(OgC.BLUE)
                .onClick(() => this.assignVariant(w.student, 'B'))
            }
            Blank()
          }
          .width('100%')
        }
      }
      .width('100%')
      .padding({ top: 8, bottom: 8 })
      .border({ width: { bottom: 1 }, color: OgC.LINE })
    }, (w: SheetWarning) => 'w' + w.student + '-' + String(w.resolved))
  }

  @Builder
  dupList() {
    ForEach(this.d !== null ? (this.d.duplicates ?? []) : [], (g: number[], gi: number) => {
      Row({ space: 8 }) {
        Text('考号 ' + this.regOfSheet(g[0]) + ' · 卷 ' + g.join(','))
          .fontSize(12).fontColor(OgC.INK).layoutWeight(1)
        ForEach(g, (sheet: number) => {
          Text('保留' + sheet)
            .fontSize(11).fontColor(OgC.BRAND)
            .border({ width: 1, color: OgC.BRAND, radius: 10 })
            .padding({ left: 8, right: 8, top: 3, bottom: 3 })
            .onClick(() => this.keepSheet(g, sheet))
        }, (sheet: number) => 'k' + sheet)
      }
      .width('100%')
      .padding({ top: 6, bottom: 6 })
      .border({ width: { bottom: 1 }, color: OgC.LINE })
    }, (g: number[], gi: number) => 'g' + gi)
  }

  @Builder
  missingList() {
    ForEach(this.missingRows(), (r: RosterRow) => {
      Row({ space: 8 }) {
        Column({ space: 1 }) {
          Text((r.name !== undefined ? r.name + ' ' : '') + r.reg).fontSize(13).fontColor(OgC.INK)
          if (r.classes !== undefined && r.classes.length > 0) {
            Text(r.classes.join('/')).fontSize(10).fontColor(OgC.INK_SUB)
          }
        }
        .alignItems(HorizontalAlign.Start)
        .layoutWeight(1)
        Text('登记缺考').fontSize(12).fontColor(OgC.WARN)
          .onClick(() => {
            this.act('缺考登记', (): Promise<Object> => {
              return OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/manual', {
                'reg': r.reg, 'absent': true
              } as Record<string, Object>);
            });
          })
        Text('补录成绩 ›').fontSize(12).fontColor(OgC.BRAND)
          .onClick(() => {
            router.pushUrl({
              url: 'pages/teacher/ManualScoresPage',
              params: { 'exam': this.examId, 'entryReg': r.reg } as Record<string, Object>
            }).catch(() => {});
          })
      }
      .width('100%')
      .padding({ top: 6, bottom: 6 })
      .border({ width: { bottom: 1 }, color: OgC.LINE })
    }, (r: RosterRow) => r.reg)
  }

  @Builder
  voidedList() {
    ForEach(this.d !== null ? (this.d.voidedRows ?? []) : [], (v: VoidRow) => {
      Row({ space: 8 }) {
        Text('卷 ' + v.student + (v.reg !== '' ? ' · ' + v.reg : '')).fontSize(12).fontColor(OgC.INK_SUB)
          .layoutWeight(1)
        Text('恢复').fontSize(12).fontColor(OgC.BRAND)
          .onClick(() => {
            this.act('恢复', (): Promise<Object> => {
              return OgNet.jpost<Record<string, string>>(
                '/api/v1/exams/' + this.examId + '/results/' + v.student + '/void', {
                  'voided': false
                } as Record<string, Object>);
            });
          })
      }
      .width('100%')
      .padding({ top: 6, bottom: 6 })
      .border({ width: { bottom: 1 }, color: OgC.LINE })
    }, (v: VoidRow) => 'v' + v.student)
  }

  @Builder
  flaggedList() {
    ForEach(this.d !== null ? (this.d.flagged ?? []) : [], (f: FlagRow) => {
      Row({ space: 8 }) {
        Text('第 ' + (this.d !== null ? (this.d.objective ?? []).length + f.question : f.question)
          + ' 题 · 卷 ' + f.student + (f.reg !== undefined && f.reg !== '' ? ' · ' + f.reg : '')
          + ' · 当前 ' + OgFormat.score(f.score) + ' 分')
          .fontSize(12).fontColor(OgC.INK).layoutWeight(1)
          .maxLines(2)
        Text('复核 ›').fontSize(12).fontColor(OgC.BRAND)
          .onClick(() => {
            router.pushUrl({
              url: 'pages/teacher/GraderPage',
              params: {
                'exam': this.examId, 'q': String(f.question), 'student': String(f.student)
              } as Record<string, Object>
            }).catch(() => {});
          })
        Text('答卷 ›').fontSize(12).fontColor(OgC.BLUE)
          .onClick(() => {
            if (f.reg !== undefined && f.reg !== '') {
              router.pushUrl({
                url: 'pages/teacher/ReviewPage',
                params: { 'exam': this.examId, 'reg': f.reg } as Record<string, string>
              }).catch(() => {});
            }
          })
        Text('清⚑').fontSize(12).fontColor(OgC.FLAG)
          .onClick(() => {
            this.act('清除标记', (): Promise<Object> => {
              return OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/flags/clear', {
                'student': f.student, 'question': f.question
              } as Record<string, number>);
            });
          })
      }
      .width('100%')
      .padding({ top: 6, bottom: 6 })
      .border({ width: { bottom: 1 }, color: OgC.LINE })
    }, (f: FlagRow) => 'f' + f.student + '-' + f.question)
  }

  @Builder
  appealList() {
    ForEach(this.d !== null ? (this.d.appeals ?? []) : [], (a: AppealView) => {
      Column({ space: 6 }) {
        Row({ space: 8 }) {
          Pill({
            text: a.status === 'resolved' ? '已回复' : '待处理',
            color: a.status === 'resolved' ? OgC.GOOD : OgC.BAD
          })
          Text((a.name !== undefined && a.name !== '' ? a.name + ' ' : '') + a.by)
            .fontSize(13).fontColor(OgC.INK)
          Blank()
          Text(a.time).fontSize(10).fontColor(OgC.INK_SUB)
        }
        .width('100%')
        Text(a.reason).fontSize(12).fontColor(OgC.INK)
        if (a.status === 'resolved' && a.reply !== undefined && a.reply !== '') {
          Text('回复:' + a.reply + '(' + a.resolvedBy + ')').fontSize(11).fontColor(OgC.BRAND_DEEP)
        }
        Row({ space: 10 }) {
          Text('查看答卷并改分 ›').fontSize(12).fontColor(OgC.BLUE)
            .onClick(() => {
              router.pushUrl({
                url: 'pages/teacher/ReviewPage',
                params: { 'exam': this.examId, 'reg': a.by } as Record<string, string>
              }).catch(() => {});
            })
          if (a.status !== 'resolved' && this.canEdit()) {
            Text('回复裁决 ›').fontSize(12).fontColor(OgC.BRAND)
              .onClick(() => {
                this.appealNo = a.n;
                this.appealReply = '';
                this.openSheet('reply');
              })
            Text('拉黑本场').fontSize(12).fontColor(OgC.BAD)
              .onClick(() => {
                this.act('拉黑', (): Promise<Object> => {
                  return OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/appeals/block', {
                    'reg': a.by, 'blocked': true, 'reason': '复核申请滥用'
                  } as Record<string, Object>);
                });
              })
            Blank()
          }
        }
        .width('100%')
      }
      .width('100%')
      .padding({ top: 8, bottom: 8 })
      .border({ width: { bottom: 1 }, color: OgC.LINE })
    }, (a: AppealView) => 'a' + a.n + '-' + a.status)
  }

  @Builder
  metaList() {
    Column({ space: 4 }) {
      Text('学科 ' + (this.d !== null && this.d.subject !== undefined ? this.d.subject : '-')
        + ' · 建卷人 ' + (this.d !== null ? this.d.createdBy : '-'))
        .fontSize(12).fontColor(OgC.INK)
      Text('日期 ' + (this.d !== null && this.d.examDate !== undefined && this.d.examDate !== ''
        ? this.d.examDate : '未填')
        + ' · 学期 ' + (this.d !== null && this.d.term !== undefined && this.d.term !== '' ? this.d.term : '未填')
        + ' · 类型 ' + (this.d !== null && this.d.examType !== undefined && this.d.examType !== ''
          ? this.d.examType : '未填'))
        .fontSize(12).fontColor(OgC.INK)
      Text('同场分组 ' + (this.d !== null && this.d.examGroup !== undefined && this.d.examGroup !== ''
        ? this.d.examGroup : '未填')
        + ' · 匿名阅卷 ' + (this.d !== null && this.d.anonymous === true ? '开' : '关'))
        .fontSize(12).fontColor(OgC.INK)
    }
    .width('100%')
    Row({ space: 10 }) {
      if (this.canEdit()) {
        Text('编辑元数据').fontSize(12).fontColor(OgC.BRAND)
          .onClick(() => {
            if (this.d === null) {
              return;
            }
          this.mExamDate = this.d.examDate ?? '';
          this.mTerm = this.d.term ?? '';
          this.mExamType = this.d.examType ?? '';
          this.mExamGroup = this.d.examGroup ?? '';
          this.mAnonymous = this.d.anonymous === true;
          this.openSheet('meta');
          })
      }
      if (this.canEdit() && this.d !== null && this.d.manualMode !== true) {
        Text('修正参考答案').fontSize(12).fontColor(OgC.BRAND)
          .onClick(() => {
            this.keyBuf = this.d !== null ? (this.d.key ?? []).join(' ') : '';
            this.openSheet('key');
          })
      }
      if (OgSession.isManager()) {
        Text('审计日志').fontSize(12).fontColor(OgC.BRAND)
          .onClick(() => {
            this.loadAudit();
            this.auditOpen = true;
          })
        Blank()
        Text('删除考试').fontSize(12).fontColor(OgC.BAD)
          .onClick(() => this.deleteExam())
      }
    }
    .width('100%')
  }

  @Builder
  exportList() {
    Flex({ wrap: FlexWrap.Wrap }) {
      Text('成绩条 PDF 预览')
        .fontSize(12)
        .fontColor(OgC.BLUE)
        .border({ width: 1, color: OgC.BLUE, radius: 12 })
        .padding({ left: 10, right: 10, top: 5, bottom: 5 })
        .margin({ right: 8, bottom: 8 })
        .onClick(() => {
          router.pushUrl({
            url: 'pages/common/PdfViewerPage',
            params: {
              'url': '/api/v1/exams/' + this.examId + '/slips.pdf',
              'title': '全班成绩条',
              'fileName': 'opengrade-slips-' + this.examId + '.pdf'
            } as Record<string, string>
          }).catch(() => {});
        })
      ForEach(this.exportItems(), (it: ExportItem) => {
        Text(it.label)
          .fontSize(12)
          .fontColor(OgC.BRAND)
          .border({ width: 1, color: OgC.BRAND, radius: 12 })
          .padding({ left: 10, right: 10, top: 5, bottom: 5 })
          .margin({ right: 8, bottom: 8 })
          .onClick(() => this.download(it))
      }, (it: ExportItem) => it.label)
    }
    .width('100%')
  }

  build() {
    Column() {
      TitleBar({
        title: this.d !== null && this.d.subject !== undefined && this.d.subject !== '' ? this.d.subject : '考试详情',
        subtitle: this.examId + (this.d !== null && this.d.examDate !== undefined ? ' · ' + this.d.examDate : ''),
        onBack: () => router.back(),
        actions: (): void => {
          this.navAction()
        }
      })
      if (this.loading) {
        LoadingView()
      } else if (this.failed) {
        ErrorView({ msg: this.failMsg, onRetry: () => this.load() })
      } else if (this.d !== null) {
        Scroll() {
          Column({ space: OgDim.GAP }) {
            this.stageCard()
            this.progressCard()
            if ((this.d.warnings ?? []).length > 0) {
              this.warningCard()
            }
            if ((this.d.duplicates ?? []).length > 0) {
              this.dupCard()
            }
            if (this.missingRows().length > 0) {
              this.missingCard()
            }
            if ((this.d.voidedRows ?? []).length > 0) {
              this.voidedCard()
            }
            if ((this.d.flagged ?? []).length > 0) {
              this.flaggedCard()
            }
            if ((this.d.appeals ?? []).length > 0) {
              this.appealCard()
            }
            this.metaCard()
            this.exportCard()
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
    .bindSheet($$this.sheetOpen, this.anySheetBuilder(), {
      // 导航分支与其他页同高;其余表单分支自适应
      height: this.sheetKind === 'nav' ? NAV_SHEET_HEIGHT : SheetSize.FIT_CONTENT,
      dragBar: true,
      onWillDisappear: () => {
        this.closeSheet();
        this.auditOpen = false;
      }
    })
  }

  private async loadAudit(): Promise<void> {
    this.auditLoading = true;
    try {
      this.audit = await OgNet.jget<AuditEntry[]>('/api/v1/exams/' + this.examId + '/audit');
    } catch (e) {
      this.audit = [];
      promptAction.showToast({ message: e instanceof OgError ? e.message : '审计加载失败' });
    }
    this.auditLoading = false;
  }

  @Builder
  auditSheet() {
    Column({ space: 8 }) {
      if (this.auditLoading) {
        LoadingView()
      } else if (this.audit.length === 0) {
        Text('暂无操作记录').fontSize(12).fontColor(OgC.INK_SUB)
      }
      ForEach(this.audit, (a: AuditEntry) => {
        Column({ space: 2 }) {
          Row({ space: 6 }) {
            Text(a.action).fontSize(13).fontWeight(FontWeight.Medium).fontColor(OgC.INK)
            Blank()
            Text(a.time).fontSize(10).fontColor(OgC.INK_SUB)
          }
          .width('100%')
          if (a.detail !== '') {
            Text(a.detail).fontSize(11).fontColor(OgC.INK_SUB)
          }
          Text('操作人 ' + a.by).fontSize(10).fontColor(OgC.INK_SUB)
        }
        .width('100%')
        .padding({ top: 6, bottom: 6 })
        .border({ width: { bottom: 1 }, color: OgC.LINE })
      }, (a: AuditEntry, i: number) => 'a' + i)
    }
    .padding(OgDim.PAD)
    .width('100%')
  }

  @Builder
  anySheetBuilder() {
    if (this.auditOpen) {
      this.auditSheet()
    } else if (this.sheetKind === 'nav') {
      StaffNavGrid({ current: 'exams', onClose: () => {
        this.closeSheet();
      } })
    } else if (this.sheetKind === 'assign') {
      this.assignSheet()
    } else if (this.sheetKind === 'meta') {
      this.metaSheet()
    } else if (this.sheetKind === 'key') {
      this.keySheet()
    } else if (this.sheetKind === 'reply') {
      this.replySheet()
    }
  }

  @Builder
  assignSheet() {
    Column({ space: 10 }) {
      Text('卷 ' + this.assignStudent + ' 的考号推荐(按"可读位匹配")')
        .fontSize(12).fontColor(OgC.INK_SUB)
      ForEach(this.suggestions, (c: RosterCandidate) => {
        Row({ space: 8 }) {
          Text((c.name !== undefined ? c.name : '') + ' ' + c.reg)
            .fontSize(14).fontColor(OgC.INK).layoutWeight(1)
          if (c.class !== undefined && c.class !== '') {
            Text(c.class).fontSize(11).fontColor(OgC.INK_SUB)
          }
        }
        .width('100%')
        .padding(10)
        .backgroundColor(this.customReg === c.reg ? OgC.BRAND_LIGHT : OgC.BG)
        .borderRadius(8)
        .onClick(() => {
          this.customReg = c.reg;
        })
      }, (c: RosterCandidate) => c.reg)
      if (this.suggestions.length === 0) {
        Text('没有可用推荐,请直接输入考号。').fontSize(12).fontColor(OgC.INK_SUB)
      }
      TextInput({ text: this.customReg, placeholder: '或手动输入考号' })
        .fontSize(14).height(40)
        .onChange((v: string) => {
          this.customReg = v;
        })
      PrimaryButton({ label: '确认指派', full: true, onTap: () => this.confirmAssign() })
    }
    .padding(OgDim.PAD)
    .width('100%')
  }

  @Builder
  metaSheet() {
    Column({ space: 10 }) {
      Column({ space: 4 }) {
        Text('考试日期(YYYY-MM-DD)').fontSize(11).fontColor(OgC.INK_SUB).width('100%')
        TextInput({ text: this.mExamDate, placeholder: '如 2026-10-24' })
          .fontSize(14).height(40)
          .onChange((v: string) => {
            this.mExamDate = v;
          })
      }.width('100%')
      Column({ space: 4 }) {
        Text('学期').fontSize(11).fontColor(OgC.INK_SUB).width('100%')
        TextInput({ text: this.mTerm, placeholder: '如 2026秋季' })
          .fontSize(14).height(40)
          .onChange((v: string) => {
            this.mTerm = v;
          })
      }.width('100%')
      Column({ space: 4 }) {
        Text('类型').fontSize(11).fontColor(OgC.INK_SUB).width('100%')
        TextInput({ text: this.mExamType, placeholder: '如期中/月考' })
          .fontSize(14).height(40)
          .onChange((v: string) => {
            this.mExamType = v;
          })
      }.width('100%')
      Column({ space: 4 }) {
        Text('同场分组(年级总表聚合键)').fontSize(11).fontColor(OgC.INK_SUB).width('100%')
        TextInput({ text: this.mExamGroup, placeholder: '如 2026期中' })
          .fontSize(14).height(40)
          .onChange((v: string) => {
            this.mExamGroup = v;
          })
      }.width('100%')
      Row({ space: 8 }) {
        Text('匿名阅卷').fontSize(13).fontColor(OgC.INK)
        Blank()
        Toggle({ type: ToggleType.Switch, isOn: this.mAnonymous })
          .selectedColor(OgC.BRAND)
          .onChange((on: boolean) => {
            this.mAnonymous = on;
          })
      }
      .width('100%')
      PrimaryButton({
        full: true,
        label: '保存',
        onTap: () => {
          this.closeSheet();
          this.act('元数据保存', (): Promise<Object> => {
            return OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/meta', {
              'examDate': this.mExamDate.trim(),
              'term': this.mTerm.trim(),
              'examType': this.mExamType.trim(),
              'examGroup': this.mExamGroup.trim(),
              'anonymous': this.mAnonymous
            } as Record<string, Object>);
          });
        }
      })
    }
    .padding(OgDim.PAD)
    .width('100%')
  }

  @Builder
  keySheet() {
    Column({ space: 10 }) {
      Text('每题一个参考答案,空格分隔(如 A T AC B…)。保存后全班客观题自动重统。')
        .fontSize(12).fontColor(OgC.INK_SUB)
      TextArea({ text: this.keyBuf, placeholder: 'A T AC B …' })
        .fontSize(14)
        .height(140)
        .onChange((v: string) => {
          this.keyBuf = v;
        })
      PrimaryButton({
        full: true,
        label: '保存并重统',
        onTap: () => {
          const arr: string[] = this.keyBuf.trim().split(/\s+/).filter((s: string) => s !== '');
          if (this.d !== null && arr.length !== this.d.key.length) {
            promptAction.showToast({
              message: '答案数 ' + arr.length + ' 与客观题数 ' + this.d.key.length + ' 不一致'
            });
            return;
          }
          this.closeSheet();
          this.act('答案修正', (): Promise<Object> => {
            return OgNet.jpost<Record<string, string>>('/api/v1/exams/' + this.examId + '/key', {
              'key': arr
            } as Record<string, string[]>);
          });
        }
      })
    }
    .padding(OgDim.PAD)
    .width('100%')
  }

  @Builder
  replySheet() {
    Column({ space: 10 }) {
      TextArea({ text: this.appealReply, placeholder: '填写复核结论回复(学生端可见)' })
        .fontSize(13)
        .height(100)
        .onChange((v: string) => {
          this.appealReply = v;
        })
      PrimaryButton({
        full: true,
        label: '回复并裁决',
        onTap: () => {
          const reply = this.appealReply.trim();
          if (reply === '') {
            promptAction.showToast({ message: '请填写回复内容' });
            return;
          }
          const no = this.appealNo;
          this.closeSheet();
          this.act('复核裁决', (): Promise<Object> => {
            return OgNet.jpost<Record<string, string>>(
              '/api/v1/exams/' + this.examId + '/appeals/' + no + '/resolve', {
                'reply': reply
              } as Record<string, Object>);
          });
        }
      })
    }
    .padding(OgDim.PAD)
    .width('100%')
  }
}

// —— 局部轻量类型 ——

interface ProgressRow {
  question: number;
  kind: string;
  max: number;
  graded: number;
  total: number;
  teacher?: string;
  elective?: string;
}

interface VoidRow {
  student: number;
  reg: string;
}

interface FlagRow {
  student: number;
  reg?: string;
  question: number;
  score: number;
}

interface ExportItem {
  label: string;
  path: string;
  name: string;
}
