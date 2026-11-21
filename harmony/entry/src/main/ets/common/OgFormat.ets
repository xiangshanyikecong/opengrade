// 展示口径格式化:与 web 端(grader/teacher/student)文案保持一致。

import { OgC } from './OgTheme';

export class OgFormat {
  // 分数显示:去掉多余尾零(12.5 / 10 / 0.5)
  static score(v: number): string {
    if (!isFinite(v)) {
      return '0';
    }
    if (Math.abs(v - Math.round(v)) < 0.0001) {
      return String(Math.round(v));
    }
    return String(Math.round(v * 100) / 100);
  }

  static pct(rate: number): string {
    return Math.round(rate * 100) + '%';
  }

  // "2026-10-24 08:00" 或 "2026-10-24" → 展示原样截断
  static date(s: string | undefined): string {
    if (!s) {
      return '';
    }
    return s;
  }

  static roleName(role: string): string {
    if (role === 'admin') {
      return '管理员';
    }
    if (role === 'leader') {
      return '年级组长';
    }
    if (role === 'teacher') {
      return '教师';
    }
    if (role === 'student') {
      return '学生';
    }
    if (role === 'parent') {
      return '家长';
    }
    return role;
  }

  static kindName(kind: string): string {
    if (kind === 'choice') {
      return '单选';
    }
    if (kind === 'judge') {
      return '判断';
    }
    if (kind === 'multiple') {
      return '多选';
    }
    if (kind === 'fillblank') {
      return '填空';
    }
    if (kind === 'subjective') {
      return '解答';
    }
    return kind;
  }

  static markName(mark: string): string {
    if (mark === 'check') {
      return '✓';
    }
    if (mark === 'cross') {
      return '✗';
    }
    if (mark === 'flag') {
      return '⚑';
    }
    return '';
  }

  static markColor(mark: string): string {
    if (mark === 'check') {
      return OgC.GOOD;
    }
    if (mark === 'cross') {
      return OgC.BAD;
    }
    if (mark === 'flag') {
      return OgC.FLAG;
    }
    return OgC.INK_SUB;
  }

  // 阶段主操作文案(对齐 teacher.html 列表主操作)
  static stageAction(stage: string, dirty: boolean): string {
    if (stage === 'awaiting_scan' || stage === 'scanning') {
      return '去扫描';
    }
    if (stage === 'anomalies') {
      return '处理异常';
    }
    if (stage === 'grading') {
      return '去阅卷';
    }
    if (stage === 'ready' || dirty) {
      return dirty ? '重新发布' : '发布成绩';
    }
    return '详情';
  }
}
