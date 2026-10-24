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
func handleMyTasks(w http.ResponseWriter, r *http.Request, store *Store, school *School, u *auth.User) {
	items := []taskItem{} // 空态给 []
	for _, ex := range store.List() {
		if !canOpenExam(u, ex, school) {
			continue
		}
		stage := examStage(ex)
		base := taskItem{Exam: ex.ID, Subject: ex.Subject,
			CreatedAt: ex.CreatedAt.Format("2006-01-02 15:04"), Stage: stage.Stage, StageLabel: stage.Label}
		objN := len(ex.Layout.Questions)

		// 按题分配
		for qi := range ex.Layout.Subjectives {
			if ex.Tasks != nil && ex.Tasks[fmt.Sprintf("q%d", qi+1)] == u.Username {
				graded := 0
				for si := range ex.Results {
					if subjectiveGraded(ex, si+1, qi+1) {
						graded++
					}
				}
				it := base
				it.Kind, it.Question = "question", qi+1
				it.Label = fmt.Sprintf("第 %d 题(满分 %g)", objN+qi+1, ex.Layout.Subjectives[qi].Score)
				it.Graded, it.Total = graded, activeCount(ex)
				it.Due = ex.TaskDue[fmt.Sprintf("q%d", qi+1)]
				items = append(items, it)
			}
		}
		// 按班分配:我被分到的班(任教班 ∪ 班主任班任一命中)
		if ex.TaskMode == "class" {
			for _, c := range append(append([]string(nil), u.AllClasses()...), u.HeadClasses...) {
				if ex.ClassTasks == nil || ex.ClassTasks[c] != u.Username {
					continue
				}
				graded, total := 0, 0
				byReg := map[string][]string{}
				for _, st := range ex.Roster {
					byReg[st.Reg] = st.AllClasses()
				}
				for si := range ex.Results {
					cls := byReg[ex.AssignedReg(si+1)]
					if cls != nil && !overlaps(cls, []string{c}) {
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
				it.Label = c + "(按班阅卷)"
				it.Graded, it.Total = graded, total
				it.Due = ex.TaskDue[c]
				items = append(items, it)
			}
		}
		// 我建的考试兜底:建卷人全程跟进 —— 可评(未评完)或可跟进(待扫/扫图中)都给入口
		if ex.CreatedBy == u.Username && ((stage.Total > 0 && stage.Ungraded > 0) || stage.Stage == stageAwaiting || stage.Stage == stageScanning) {
			it := base
			it.Kind = "mine"
			switch {
			case stage.Total > 0 && stage.Ungraded > 0:
				it.Label = "我创建的考试(未分完/未评完)"
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
