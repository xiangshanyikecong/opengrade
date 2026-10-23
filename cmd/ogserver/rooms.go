package main

// 考场编排(走班排考的排场环节):
//   POST   /api/v1/exams/{id}/rooms  {"size":30,"group":"class"} 生成方案(同班聚齐/纯考号序)
//   PATCH  /api/v1/exams/{id}/rooms  {"swap":[a,b]} / {"proctor":{...}} 座位对调/监考指派
//   GET    /api/v1/exams/{id}/rooms  读取方案(前端渲染座位表并打印)
// 生成与微调限管理员/年级组长/建卷教师;查看放开给可见考试的教职员。

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/roster"
)

// handleRoomsGenerate POST:按名册生成分场方案并落盘。
func handleRoomsGenerate(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false)
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !canEditExam(u, ex) {
		httpError(w, http.StatusForbidden, fmt.Errorf("仅管理员/年级组长/建卷教师可生成考场编排"))
		return
	}
	var req struct {
		Size  int    `json:"size"`
		Group string `json:"group"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if req.Size < 1 || req.Size > 500 {
		httpError(w, http.StatusBadRequest, fmt.Errorf("每场人数必须在 1~500 之间"))
		return
	}
	if req.Group != "reg" {
		req.Group = "class" // 缺省同班聚齐
	}
	plan := generateRoomPlan(ex.Roster, req.Size, req.Group)
	if len(plan.Rooms) == 0 {
		httpError(w, http.StatusUnprocessableEntity, fmt.Errorf("名册为空,无法编排考场"))
		return
	}
	if err := store.SetRooms(ex.ID, plan, u.Username); err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// handleRoomsPatch PATCH:座位对调或监考指派(微调,不重排)。
func handleRoomsPatch(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false)
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !canEditExam(u, ex) {
		httpError(w, http.StatusForbidden, fmt.Errorf("仅管理员/年级组长/建卷教师可调整考场"))
		return
	}
	var req struct {
		Swap    *swapReq    `json:"swap"`
		Proctor *proctorReq `json:"proctor"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	switch {
	case req.Swap != nil:
		a, b := req.Swap.A, req.Swap.B
		err := store.MutateRooms(ex.ID, u.Username, func(p *RoomPlan) (string, error) {
			sa := findSeat(p, a.Room, a.Idx)
			sb := findSeat(p, b.Room, b.Idx)
			if sa == nil || sb == nil {
				return "", fmt.Errorf("座位不存在:%s %d / %s %d", a.Room, a.Idx, b.Room, b.Idx)
			}
			sa.Reg, sb.Reg = sb.Reg, sa.Reg
			sa.Name, sb.Name = sb.Name, sa.Name
			sa.Class, sb.Class = sb.Class, sa.Class
			return fmt.Sprintf("对调座位 %s#%d ↔ %s#%d", a.Room, a.Idx, b.Room, b.Idx), nil
		})
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
	case req.Proctor != nil:
		err := store.MutateRooms(ex.ID, u.Username, func(p *RoomPlan) (string, error) {
			room := findRoom(p, req.Proctor.Room)
			if room == nil {
				return "", fmt.Errorf("考场 %s 不存在", req.Proctor.Room)
			}
			room.Proctor = req.Proctor.Teacher
			return fmt.Sprintf("考场 %s 监考 → %s", req.Proctor.Room, req.Proctor.Teacher), nil
		})
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
	default:
		httpError(w, http.StatusBadRequest, fmt.Errorf("请求体需包含 swap 或 proctor"))
		return
	}
	exNow, _ := store.Get(ex.ID)
	writeJSON(w, http.StatusOK, exNow.Rooms)
}

// handleRoomsGet GET:读取编排方案。
func handleRoomsGet(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false)
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if ex.Rooms == nil {
		writeJSON(w, http.StatusOK, map[string]any{"rooms": nil})
		return
	}
	writeJSON(w, http.StatusOK, ex.Rooms)
}

// ---- 请求辅助类型与工具函数 ----

type seatRef struct {
	Room string `json:"room"`
	Idx  int    `json:"idx"`
}
type swapReq struct {
	A seatRef `json:"a"`
	B seatRef `json:"b"`
}
type proctorReq struct {
	Room    string `json:"room"`
	Teacher string `json:"teacher"`
}

func findRoom(p *RoomPlan, name string) *Room {
	for i := range p.Rooms {
		if p.Rooms[i].Name == name {
			return &p.Rooms[i]
		}
	}
	return nil
}

func findSeat(p *RoomPlan, room string, idx int) *Seat {
	rm := findRoom(p, room)
	if rm == nil {
		return nil
	}
	for i := range rm.Seats {
		if rm.Seats[i].Idx == idx {
			return &rm.Seats[i]
		}
	}
	return nil
}

// canEditExam 管理员/年级组长/建卷教师可变更考试级配置(与 /key 同口径)。
func canEditExam(u *auth.User, ex *Exam) bool {
	return u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || ex.CreatedBy == u.Username
}

// generateRoomPlan 按名册生成考场方案:
//   - group=class:按主班(Classes[0],走班名册可把教学班放为主班)排序聚齐,班内按考号;
//   - group=reg :全部按考号顺序;
//   - 依次装满每场 size 人;座位号场内 1..n。
func generateRoomPlan(students []roster.Student, size int, group string) *RoomPlan {
	ordered := make([]roster.Student, len(students))
	copy(ordered, students)
	sort.SliceStable(ordered, func(i, j int) bool {
		if group == "class" {
			ci, cj := ordered[i].Class, ordered[j].Class
			if ci != cj {
				return ci < cj
			}
		}
		return ordered[i].Reg < ordered[j].Reg
	})
	plan := &RoomPlan{Size: size, Group: group}
	roomNo := 0
	for start := 0; start < len(ordered); start += size {
		end := start + size
		if end > len(ordered) {
			end = len(ordered)
		}
		roomNo++
		room := Room{Name: fmt.Sprintf("R%02d", roomNo)}
		for i, st := range ordered[start:end] {
			room.Seats = append(room.Seats, Seat{
				Idx: i + 1, Reg: st.Reg, Name: st.Name, Class: st.Class,
			})
		}
		plan.Rooms = append(plan.Rooms, room)
	}
	return plan
}
