// 与 cmd/ogserver Go struct 逐字段对齐的 API 类型定义(JSON 键名同源)。
// 所有列表型字段服务端保证返回 [](非 null),客户端仍做空值兜底。

// ---------- 鉴权 ----------

export interface LoginResp {
  username: string;
  displayName: string;
  role: string;
  redirect: string;
  mustChangePassword: boolean;
  token?: string; // 仅请求携带 native:true 时返回(鸿蒙端以 Bearer 头携带)
}

export interface Me {
  username: string;
  displayName: string;
  role: string; // admin | leader | teacher | student | parent
  reg?: string;
  class?: string;
  classes?: string[];
  children?: string[];
  mustChangePassword?: boolean;
}

// ---------- 教职员:考试 ----------

export interface ExamSummary {
  id: string;
  createdAt: string;
  createdBy: string;
  subject?: string;
  examDate?: string;
  term?: string;
  examType?: string;
  examGroup?: string;
  students: number;
  objective: number;
  subjective: number;
  published: boolean;
  mine: boolean;
  stage: string; // awaiting_scan | scanning | anomalies | grading | ready | published
  stageLabel: string;
  expected: number;
  warnings: number;
  ungraded: number;
  total: number;
  dirty: boolean;
  appeals?: number;
  flagged?: number;
}

export interface QuestionSpec {
  kind: string; // choice | judge | multiple
  options?: number;
  score?: number;
  knowledge?: string;
  bankId?: string;
}

export interface SubjectivePart {
  label?: string;
  score: number;
}

export interface SubjectiveSpec {
  kind: string; // fillblank | subjective
  label?: string;
  score?: number;
  heightMM?: number;
  knowledge?: string;
  parts?: SubjectivePart[];
  elective?: string;
  bankId?: string;
}

export interface SessionDef {
  name: string;
  start: string;
  end: string;
}

export interface StageView {
  stage: string;
  label: string;
  students: number;
  expected: number;
  warnings: number;
  absent: number;
  manual: number;
  ungraded: number;
  total: number;
  gradedAll: boolean;
  dirty: boolean;
}

export interface QuestionProgress {
  question: number;
  kind: string;
  max: number;
  graded: number;
  total: number;
  teacher?: string;
  elective?: string;
}

export interface StaffInfo {
  username: string;
  displayName: string;
  role: string;
}

export interface AbsentInfo {
  student: number;
  reg: string;
}

export interface SheetWarning {
  student: number;
  reg?: string;
  texts: string[];
  resolved: boolean;
  soft: boolean;
  variant?: string;
}

export interface AppealView {
  n: number;
  time: string;
  by: string;
  name?: string;
  reason: string;
  status: string; // open | resolved
  reply?: string;
  resolvedBy?: string;
  resolvedAt?: string;
}

export interface RosterRow {
  reg: string;
  name?: string;
  classes?: string[];
}

export interface FlaggedView {
  student: number;
  reg?: string;
  question: number;
  score: number;
}

export interface ExamDetail {
  id: string;
  createdAt: string;
  createdBy: string;
  subject?: string;
  students: number;
  objective: QuestionSpec[];
  key: string[];
  subjective: SubjectiveSpec[];
  progress: QuestionProgress[];
  tasks?: Record<string, string>;
  taskMode?: string;
  classTasks?: Record<string, string>;
  taskDue?: Record<string, string>;
  classes?: string[];
  threshold: string;
  published: boolean;
  canEditKey: boolean;
  staff: StaffInfo[];
  absent: AbsentInfo[];
  manual: number;
  warnings: SheetWarning[];
  stage: StageView;
  flagged: FlaggedView[];
  scannedReg: Record<string, boolean>;
  roster: RosterRow[];
  examDate?: string;
  term?: string;
  examType?: string;
  examGroup?: string;
  session?: string;
  sessions?: SessionDef[];
  anonymous?: boolean;
  manualMode?: boolean;
  manualFull?: number;
  manualScores?: Record<string, number>;
  duplicates?: number[][]; // 同考号多卷:每组成员为卷号(1-based)
  voidedRows?: AbsentInfo[];
  appeals?: AppealView[];
  appealBlocked?: Record<string, string>;
  appealBanned?: Record<string, string>;
  hasB?: boolean;
  shortID?: string;
  keyB?: string[];
  orderB?: number[];
  regOf?: Record<string, string>;
  pages?: number;
}

// ---------- 阅卷(Grader) ----------

export interface Stroke {
  color: string; // red | green
  pts: number[]; // x0,y0,x1,y1,… 归一化 0~1
}

export interface PartState {
  part: number;
  score: number;
  graded: boolean;
  ver?: number;
}

export interface ElectiveView {
  group: number[];
  labels: string[];
  marks: number[];
  chosen: number;
}

export interface SliceItem {
  student: number;
  reg: string;
  regValid: boolean;
  url: string;
  graded: boolean;
  score: number;
  ver?: number;
  mark?: string;
  ink?: Stroke[];
  missing?: boolean;
  parts?: PartState[];
  elective?: ElectiveView;
}

export interface GradeResp {
  status: string;
  ver: number;
}

export interface GradeCurrent {
  score: number;
  ver: number;
  by: string;
}

export interface GradeConflict {
  error: string;
  current?: GradeCurrent;
}

// ---------- 学生/家长端 ----------

export interface StudentExamItem {
  id: string;
  createdAt: string;
  examDate?: string;
  child?: string;
  subject?: string;
  found: boolean;
  absent: boolean;
  score: number;
  total: number;
  rank: number;
  classMean: number;
  hasReply?: boolean;
  hasOpen?: boolean;
}

export interface StudentQuestion {
  index: number;
  kind: string;
  max: number;
  myAnswer?: string;
  correct?: string;
  right?: boolean;
  myScore: number;
  wrong: boolean;
  pending?: boolean;
  elective?: string;
  notCounted?: boolean;
}

export interface ClassStat {
  class: string;
  count: number;
  mean: number;
  meanRate: number;
}

export interface StudentSliceRef {
  question: string;
  url: string;
}

export interface StudentAppealItem {
  time: string;
  reason: string;
  status: string;
  reply?: string;
}

export interface KnowledgeStat {
  name: string;
  questions: number;
  max: number;
  earned: number;
  rate: number;
  answers: number;
}

export interface StudentExamDetail {
  id: string;
  createdAt: string;
  subject?: string;
  reg: string;
  class?: string;
  absent?: boolean;
  objective: number;
  subjective: number;
  score: number;
  total: number;
  rank: number;
  classRank: number;
  count: number;
  mean: number;
  regValid: boolean;
  questions: StudentQuestion[];
  wrongQuestions: number[];
  classRates: Record<string, number>;
  byClass: ClassStat[];
  slices?: StudentSliceRef[];
  knowledge: KnowledgeStat[];
  appeals?: StudentAppealItem[];
  pageURL?: string;
}

export interface TrendPoint {
  examId: string;
  date: string;
  subject?: string;
  absent: boolean;
  score: number;
  total: number;
  rate: number;
  rank: number;
  classRank?: number;
  mean?: number;
  classMean?: number;
}

export interface StudentTrendsResp {
  reg: string;
  class?: string;
  points: TrendPoint[];
}

export interface PracticeResult {
  title: string;
  questions: number[];
  objective: number;
  subjective: number;
}

// ---------- 学情分析 ----------

export interface ScoreSegment {
  low: number;
  high: number;
  count: number;
}

export interface ExamStats {
  count: number;
  total: number;
  mean: number;
  meanRate: number;
  max: number;
  min: number;
  median: number;
  stdDev: number;
  range: number;
  passRate: number;
  goodRate: number;
  lowRate: number;
  passLine: number;
  goodLine: number;
  lowLine: number;
  segments: ScoreSegment[];
}

export interface PartStat {
  label: string;
  score: number;
  max: number;
  count: number;
}

export interface QuestionStatView {
  index: number;
  kind: string;
  max: number;
  correct?: number;
  attempted?: number;
  mean?: number;
  graded?: number;
  count: number;
  rate: number;
  difficulty: number;
  discrimination?: number;
  options?: Record<string, number>;
  parts?: PartStat[];
  elective?: string;
  chosen?: number;
  label: string;
}

export interface AnalyticsResp {
  id: string;
  subject?: string;
  total: number;
  stats: ExamStats;
  byClass: ClassStat[];
  questions: QuestionStatView[];
  knowledge: KnowledgeStat[];
  perClassQuestion: Record<string, number[]>;
}

// ---------- 纵向趋势(教职员) ----------

export interface TrendExam {
  id: string;
  date: string;
  subject?: string;
  students: number;
  total: number;
  mean: number;
  classMean: number;
  classCount?: number;
}

export interface TrendStudentView {
  reg: string;
  name?: string;
  class?: string;
  points: TrendPoint[];
}

export interface StaffTrendsResp {
  classes: string[];
  class: string;
  subject: string;
  exams: TrendExam[];
  students: TrendStudentView[];
}

// ---------- 年级总表 ----------

export interface AggregateExam {
  id: string;
  subject?: string;
  date: string;
  total: number;
  mean: number;
  count: number;
  absent: number;
}

export interface AggregateCell {
  score: number;
  absent?: boolean;
  found: boolean;
}

export interface AggregateStudent {
  reg: string;
  name?: string;
  class?: string;
  cells: AggregateCell[];
  total: number;
  rank: number;
  classRank?: number;
}

export interface AggregateResp {
  group: string;
  exams: AggregateExam[];
  totalExams: number;
  pendingExams?: string[];
  students: AggregateStudent[];
}

// ---------- 我的任务 / 工作量 ----------

export interface TaskItem {
  exam: string;
  subject?: string;
  createdAt: string;
  stage: string;
  stageLabel: string;
  kind: string; // question | class | mine
  question?: number;
  label: string;
  graded: number;
  total: number;
  due?: string;
}

// ---------- 题库(只读) ----------

export interface BankQuestion {
  id: string;
  subject?: string;
  kind: string;
  options?: number;
  score?: number;
  stem?: string;
  stemImage?: string;
  answer?: string;
  knowledge?: string;
  difficulty?: string;
  usedIn?: string[];
  createdBy?: string;
  createdAt: string;
}

// ---------- 账号管理 ----------

export interface PublicUser {
  username: string;
  displayName: string;
  role: string;
  reg?: string;
  class?: string;
  classes?: string[];
  subjects?: string[];
  headClasses?: string[];
  subjectLead?: string[];
  grades?: string[];
  children?: string[];
  mustChangePassword?: boolean;
}

export interface StudentRow {
  reg: string;
  name: string;
  classes: string[];
  grade?: string;
}

// ---------- 复核(学生个体视图,教师端) ----------

export interface SubjectiveState {
  question: number;
  kind: string;
  label?: string;
  max: number;
  graded: boolean;
  score: number;
  ver?: number;
  mark?: string;
  parts?: PartState[];
  ink?: Stroke[];
  elective?: string;
  sliceURL?: string;
}

export interface StaffStudentDetail {
  id: string;
  createdAt: string;
  examDate?: string;
  subject?: string;
  title: string;
  reg: string;
  name?: string;
  classes: string[];
  student: number;
  published: boolean;
  dirty: boolean;
  anonymous: boolean;
  manualMode: boolean;
  canEdit: boolean;
  objN: number;
  appealBlocked?: Record<string, Object>;
  appeals?: StudentAppealItem[];
  absent?: boolean;
  objective?: number;
  subjective?: number;
  score?: number;
  total?: number;
  rank?: number;
  mean?: number;
  count?: number;
  questions?: StudentQuestion[];
  wrongQuestions?: number[];
  subjectives?: SubjectiveState[];
  pages?: number;
  pageURL?: string;
}

// ---------- 考场编排 / 场次 ----------

export interface Seat {
  idx: number;
  reg: string;
  name?: string;
  class?: string;
}

export interface Room {
  name: string;
  teacher?: string;
  seats: Seat[];
}

export interface RoomsPlan {
  size: number;
  group: string;
  rooms: Room[];
}

export interface ProctorConflict {
  teacher: string;
  examId: string;
  subject?: string;
  date: string;
  session: string;
  room: string;
  otherRoom: string;
}

export interface RoomsResp {
  size: number;
  group: string;
  rooms: Room[];
  conflicts: ProctorConflict[];
}

export interface AppealBan {
  reg: string;
  reason?: string;
  by: string;
  time: string;
}

export interface SuggestResp {
  student: number;
  reg: string;
  regValid: boolean;
  regMarks?: number[];
  assigned?: boolean;
  suggestions?: RosterCandidate[];
}

export interface RosterCandidate {
  reg: string;
  name?: string;
  class?: string;
  score?: number;
}
