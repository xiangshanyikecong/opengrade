package main

// 账号批量导入:POST /api/v1/users/import {"csv":"...","defaultPassword":"..."}
// CSV 每行一个账号(UTF-8,Excel 可另存为"CSV UTF-8"),列序固定:
//
//	用户名,姓名,角色,密码,考号,班级,学科,管辖年级,绑定子女
//
// 角色支持中英文别名(学生/教师/家长/组长/管理员);多值字段用竖线分隔(如 数学|物理)。
// 首行为表头(含"用户名"字样)时自动跳过。已存在的账号只更新行内给出的非空字段。

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"

	"github.com/opengrade/opengrade/auth"
)

// handleUserImport 批量导入账号(仅管理员)。
func handleUserImport(w http.ResponseWriter, r *http.Request, authStore *auth.Store, u *auth.User) {
	var req struct {
		CSV             string `json:"csv"`
		DefaultPassword string `json:"defaultPassword"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	text := strings.TrimPrefix(req.CSV, "\xEF\xBB\xBF") // Excel UTF-8 BOM
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1 // 各行列数可不同(允许只填前几列)
	records, err := reader.ReadAll()
	if err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("CSV 解析失败: %w", err))
		return
	}
	type lineFail struct {
		Line  int    `json:"line"` // 数据行号(1-based,不含表头)
		Error string `json:"error"`
	}
	out := struct {
		Created int        `json:"created"`
		Updated int        `json:"updated"`
		Failed  []lineFail `json:"failed"`
	}{Failed: []lineFail{}}

	for i, rec := range records {
		if len(rec) == 0 || (len(rec) == 1 && strings.TrimSpace(rec[0]) == "") {
			continue // 空行
		}
		lineNo := i + 1
		if i == 0 && (strings.Contains(rec[0], "用户名") ||
			strings.EqualFold(strings.TrimSpace(rec[0]), "username")) {
			continue // 表头(中文或英文)
		}
		cell := func(k int) string { // 越界列按空串处理,允许只填前几列
			if k < len(rec) {
				return strings.TrimSpace(rec[k])
			}
			return ""
		}
		split := func(k int) []string {
			var out []string
			for _, p := range strings.Split(cell(k), "|") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
			return out
		}
		row := &auth.User{
			Username:    cell(0),
			DisplayName: cell(1),
			Role:        roleAlias(cell(2)),
			Reg:         cell(4),
			Class:       cell(5),
			Classes:     split(5), // 班级列支持"行政班|教学班"(走班双归属);单值即 [Class]
			Subjects:    split(6),
			Grades:      split(7),
			Children:    split(8),
		}
		if len(row.Classes) == 0 && row.Class != "" {
			row.Classes = []string{row.Class}
		}
		password := cell(3)
		if password == "" {
			password = req.DefaultPassword
		}
		created, err := authStore.ImportUpsert(row, password)
		if err != nil {
			out.Failed = append(out.Failed, lineFail{lineNo, fmt.Errorf("第 %d 行(%s):%w", lineNo, row.Username, err).Error()})
			continue
		}
		if created {
			out.Created++
		} else {
			out.Updated++
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// roleAlias 把角色中英文别名归一为存储值;未知值原样返回(由 ImportUpsert 报错)。
func roleAlias(s string) auth.Role {
	switch strings.TrimSpace(s) {
	case "学生":
		return auth.RoleStudent
	case "教师":
		return auth.RoleTeacher
	case "家长":
		return auth.RoleParent
	case "组长", "年级组长":
		return auth.RoleLeader
	case "管理员":
		return auth.RoleAdmin
	default:
		return auth.Role(s)
	}
}
