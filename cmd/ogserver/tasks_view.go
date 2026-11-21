package main

// 我的阅卷任务聚合(常规流程:教师从"我的任务"进入评卷,不在考试列表里自己找):
//   - 按题分配:该题阅卷人是我 → 一条任务,含本题进度;
//   - 按班分配:我被分到某班 → 一条任务,进度按该班学生的全部主观题汇总;
//   - 我创建的考试:主观题未评完 → 兜底任务(建卷人总得有入口改自己的卷);
// 每条任务带考试阶段,前端按"进行中/可开始"排序呈现。

import (
	"fmt"
	"net/http"

	"github.com/opengrade/opengrade/auth"
)

// taskItem 是我的任务列表一行。
type taskItem struct {
	Exam       string `json:"exam"`
	Subject    string `json:"subject,omitempty"`
	CreatedAt  string `json:"createdAt"`
	Stage      string `json:"stage"`
	StageLabel string `json:"stageLabel"`
	Kind       string `json:"kind"`               // question=按题 | class=按班 | mine=我建的考试
	Question   int    `json:"question,omitempty"` // kind=question:主观题号(1-based)
	Label      string `json:"label"`              // 展示名:第 N 题 / 班级名 / 建卷人入口
	Graded     int    `json:"graded"`
	Total      int    `json:"total"`
	Due        string `json:"due,omitempty"` // 截止日期(YYYY-MM-DD;按题=q{n},按班=班级名)
}

// handleMyTasks GET /api/v1/my/tasks(教职员):聚合当前账号在各场考试的阅卷任务。
// 摘要粗筛(全量轻量索引,零全量加载)→ 命中的考试才按需加载算精确进度:
// 任务归属判定只用 Tasks/ClassTasks/TaskMode/CreatedBy/Stage,全在摘要里。
func handleMyTasks(w http.ResponseWriter, r *http.Request, store *Store, school *School, u *auth.User) {
	items := []taskItem{} // 空态给 []
	type candidate struct {
		sm          *ExamSummary
		questionals map[int]bool // 按题分配命中的主观题号(1-based);nil=无
		classes     []string     // 按班分配命中的班级
		mine        bool
	}
	var cands []candidate
	store.VisitSummaries(func(sm *ExamSummary) {
		if !canOpenExamView(u, sm.identity(), school) {
			return
		}
		c := candidate{sm: sm}
		if sm.Tasks != nil {
			for qi := 1; qi <= sm.Subjective; qi++ {
				if sm.Tasks[fmt.Sprintf("q%d", qi)] == u.Username {
					if c.questionals == nil {
						c.questionals = map[int]bool{}
					}
					c.questionals[qi] = true
				}
			}
		}
		if sm.TaskMode == "class" && sm.ClassTasks != nil {
			seenCls := map[string]bool{}
			for _, c2 := range append(append([]string(nil), u.AllClasses()...), u.HeadClasses...) {
				// 任教班与班主任班可能重合:去重,否则同一任务返回两行(评审 tasks_view.go:59)
				if sm.ClassTasks[c2] == u.Username && !seenCls[c2] {
					seenCls[c2] = true
					c.classes = append(c.classes, c2)
				}
			}
		}
		st := sm.Stage
		c.mine = sm.CreatedBy == u.Username && ((st.Total > 0 && st.Ungraded > 0) ||
			st.Stage == stageAwaiting || st.Stage == stageScanning || st.Stage == stageAnomaly)
		if len(c.questionals) > 0 || len(c.classes) > 0 || c.mine {
			cands = append(cands, c)
		}
	})
	for _, c := range cands {
		ex, ok := store.Get(c.sm.ID)
		if !ok {
			continue // 盘上缺失(外部破坏):跳过,与启动口径一致
		}
		// 摘要粗筛与加载之间任务可能被改派/撤回:用载入快照重新确认可见性与归属,
		// 进度/截止日期一律以新快照为准(评审 tasks_view.go:37)
		if !canOpenExam(u, ex, school) {
			continue
		}
		questionals := map[int]bool{}
		if ex.Tasks != nil {
			for qi := 1; qi <= len(ex.Layout.Subjectives); qi++ {
				if ex.Tasks[fmt.Sprintf("q%d", qi)] == u.Username {
					questionals[qi] = true
				}
			}
		}
		var classes []string
		if ex.TaskMode == "class" && ex.ClassTasks != nil {
			seenCls := map[string]bool{}
			for _, c2 := range append(append([]string(nil), u.AllClasses()...), u.HeadClasses...) {
				if ex.ClassTasks[c2] == u.Username && !seenCls[c2] {
					seenCls[c2] = true
					classes = append(classes, c2)
				}
			}
		}
		stage := examStage(ex)
		mine := ex.CreatedBy == u.Username &&
			((stage.Total > 0 && stage.Ungraded > 0) ||
				stage.Stage == stageAwaiting || stage.Stage == stageScanning || stage.Stage == stageAnomaly)
		if len(questionals) == 0 && len(classes) == 0 && !mine {
			continue // 载入后任务已不归我(改派/撤回/已评完):剔除
		}
		base := taskItem{Exam: ex.ID, Subject: ex.Subject,
			CreatedAt: ex.CreatedAt.Format("2006-01-02 15:04"), Stage: stage.Stage, StageLabel: stage.Label}
		objN := len(ex.Layout.Questions)

		// 按题分配(归属以载入快照为准)
		for qi := range ex.Layout.Subjectives {
			if !questionals[qi+1] {
				continue
			}
			graded := 0
			for si := range ex.Results {
				if ex.Results[si] == nil || ex.Results[si].Voided || ex.Results[si].Absent {
					continue // 缺考占位卷不进任务口径
				}
				if subjectiveGraded(ex, si+1, qi+1) {
					graded++
				}
			}
			it := base
			it.Kind, it.Question = "question", qi+1
			it.Label = fmt.Sprintf("第 %d 题(满分 %g)", objN+qi+1, ex.Layout.Subjectives[qi].Score)
			it.Graded, it.Total = graded, gradableCount(ex)
			it.Due = ex.TaskDue[fmt.Sprintf("q%d", qi+1)]
			items = append(items, it)
		}
		// 按班分配:我被分到的班(任教班 ∪ 班主任班任一命中,已去重)
		for _, cls := range classes {
			graded, total := 0, 0
			byReg := map[string][]string{}
			for _, st := range ex.Roster {
				byReg[st.Reg] = st.AllClasses()
			}
			for si := range ex.Results {
				res := ex.Results[si]
				if res == nil || res.Voided || res.Absent {
					continue // 缺考占位卷不进任务口径
				}
				clsOf := byReg[ex.AssignedReg(si+1)]
				if clsOf != nil && !overlaps(clsOf, []string{cls}) {
					continue // 名册覆盖但不是本班:不计入我的任务口径
				}
				for qi := range ex.Layout.Subjectives {
					total++
					if subjectiveGraded(ex, si+1, qi+1) {
						graded++
					}
				}
			}
			it := base
			it.Kind = "class"
			it.Label = cls + "(按班阅卷)"
			it.Graded, it.Total = graded, total
			it.Due = ex.TaskDue[cls]
			items = append(items, it)
		}
		// 我建的考试兜底:建卷人全程跟进 —— 可评(未评完)、待扫/扫图中、异常待裁决都给入口
		if mine {
			it := base
			it.Kind = "mine"
			switch {
			case stage.Total > 0 && stage.Ungraded > 0:
				it.Label = "我创建的考试(未分完/未评完)"
			case stage.Stage == stageAnomaly:
				it.Label = fmt.Sprintf("我创建的考试(异常待处理 %d:重答/空白卡/残卷待裁决)", stage.Warnings)
			case stage.Stage == stageAwaiting:
				it.Label = "我创建的考试(待扫描:考后上传扫描件)"
			default:
				it.Label = "我创建的考试(扫描进行中)"
			}
			it.Graded, it.Total = stage.Total-stage.Ungraded, stage.Total
			items = append(items, it)
		}
	}
	writeJSON(w, http.StatusOK, items)
}
