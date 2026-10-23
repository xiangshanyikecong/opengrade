package main

// 考场编排端到端:生成(同班聚齐/纯考号序)→ 座位对调 → 监考指派 → 权限 → 重启持久化。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoomsE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")
	teacher := newClient(t, ts.URL)
	teacher.login("teacher1", "teach123")

	// 建考(admin 为建卷人;名册 5 人跨两班,走班双归属一人)
	layout, key, _, scans := codeE2EFixture(t, "fill")
	rosterCSV := "考号,姓名,班级\n" +
		"00000001,甲,三年二班\n00000002,乙,三年二班\n00000003,丙,三年一班\n" +
		"00000004,丁,三年一班|物理走班A\n00000005,戊,三年二班\n"
	var body bytes.Buffer
	files := make([]multipartFile, len(scans))
	for i, s := range scans {
		files[i] = multipartFile{field: "images", name: fmt.Sprintf("r_%02d.jpg", i+1), ctype: "image/jpeg", data: s}
	}
	writeExamForm(&body, layout, key, rosterCSV, files...)
	resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建考失败 %d: %s", resp.StatusCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	examID, _ := m["id"].(string)

	// ---------- 1. 生成:每场 2 人、同班聚齐 ----------
	var plan *RoomPlan
	t.Run("A_生成", func(t *testing.T) {
		plan = roomsPost(t, admin, examID, map[string]any{"size": 2, "group": "class"})
		if len(plan.Rooms) != 3 {
			t.Fatalf("考场数 = %d,期望 3(5 人每场 2 人)", len(plan.Rooms))
		}
		// 同班聚齐:班级按字典序,三年一班(丙/丁)先装 R01,三年二班装 R02,戊落尾场
		r1 := plan.Rooms[0]
		if r1.Name != "R01" || len(r1.Seats) != 2 || r1.Seats[0].Class != "三年一班" || r1.Seats[1].Class != "三年一班" {
			t.Fatalf("R01 = %+v", r1)
		}
		r2 := plan.Rooms[1]
		if len(r2.Seats) != 2 || r2.Seats[0].Reg != "00000001" || r2.Seats[1].Reg != "00000002" {
			t.Fatalf("R02 = %+v(三年二班应按考号序)", r2)
		}
		r3 := plan.Rooms[2]
		if len(r3.Seats) != 1 || r3.Seats[0].Reg != "00000005" {
			t.Fatalf("R03 = %+v(尾场应只剩戊)", r3)
		}
		if r3.Seats[0].Idx != 1 {
			t.Fatalf("场内座位号应从 1 起,得到 %d", r3.Seats[0].Idx)
		}
	})

	// ---------- 2. 座位对调 ----------
	t.Run("B_座位对调", func(t *testing.T) {
		regA := plan.Rooms[0].Seats[0].Reg // R01#1
		regB := plan.Rooms[2].Seats[0].Reg // R03#1
		plan = roomsPatch(t, admin, examID, map[string]any{
			"swap": map[string]any{
				"a": map[string]any{"room": "R01", "idx": 1},
				"b": map[string]any{"room": "R03", "idx": 1},
			},
		})
		if plan.Rooms[0].Seats[0].Reg != regB || plan.Rooms[2].Seats[0].Reg != regA {
			t.Fatalf("对调失败:R01#1=%s R03#1=%s", plan.Rooms[0].Seats[0].Reg, plan.Rooms[2].Seats[0].Reg)
		}
		if plan.Rooms[0].Seats[0].Name == "" || plan.Rooms[2].Seats[0].Name == "" {
			t.Fatal("对调应连同姓名/班级一起搬移")
		}
	})

	// ---------- 3. 监考指派 ----------
	t.Run("C_监考指派", func(t *testing.T) {
		plan = roomsPatch(t, admin, examID, map[string]any{
			"proctor": map[string]any{"room": "R01", "teacher": "teacher1"},
		})
		if plan.Rooms[0].Proctor != "teacher1" {
			t.Fatalf("R01 监考 = %q", plan.Rooms[0].Proctor)
		}
	})

	// ---------- 4. 权限:非建卷教师不可生成/微调,可查看 ----------
	t.Run("D_权限", func(t *testing.T) {
		if got := teacher.code("POST", "/api/v1/exams/"+examID+"/rooms", jsonBody(map[string]any{"size": 10}), "application/json"); got != http.StatusForbidden {
			t.Fatalf("非建卷教师生成应 403,得到 %d", got)
		}
		if got := teacher.code("PATCH", "/api/v1/exams/"+examID+"/rooms", jsonBody(map[string]any{"proctor": map[string]any{"room": "R01", "teacher": "teacher2"}}), "application/json"); got != http.StatusForbidden {
			t.Fatalf("非建卷教师微调应 403,得到 %d", got)
		}
		got := teacher.json("GET", "/api/v1/exams/"+examID+"/rooms", nil, http.StatusOK)
		if got["size"].(float64) != 2 {
			t.Fatalf("查看编排失败:%v", got)
		}
	})

	// ---------- 5. 重启持久化 ----------
	t.Run("E_重启持久化", func(t *testing.T) {
		srv2, err := newServer(dir, false)
		if err != nil {
			t.Fatal(err)
		}
		ts2 := httptest.NewServer(srv2.Handler)
		defer ts2.Close()
		a2 := newClient(t, ts2.URL)
		a2.login("admin", "admin123")
		m := a2.json("GET", "/api/v1/exams/"+examID+"/rooms", nil, http.StatusOK)
		rooms, _ := m["rooms"].([]any)
		if len(rooms) != 3 {
			t.Fatalf("重启后考场数 = %d,期望 3", len(rooms))
		}
		r1, _ := rooms[0].(map[string]any)
		if r1["proctor"] != "teacher1" {
			t.Fatalf("重启后监考指派丢失:%v", r1["proctor"])
		}
	})
}

func roomsPost(t *testing.T, c *e2eClient, id string, body map[string]any) *RoomPlan {
	t.Helper()
	m := c.json("POST", "/api/v1/exams/"+id+"/rooms", body, http.StatusOK)
	return jsonToPlan(t, m)
}

func roomsPatch(t *testing.T, c *e2eClient, id string, body map[string]any) *RoomPlan {
	t.Helper()
	m := c.json("PATCH", "/api/v1/exams/"+id+"/rooms", body, http.StatusOK)
	return jsonToPlan(t, m)
}

func jsonToPlan(t *testing.T, m map[string]any) *RoomPlan {
	t.Helper()
	rooms, _ := m["rooms"].([]any)
	plan := &RoomPlan{Size: int(m["size"].(float64))}
	group, _ := m["group"].(string)
	plan.Group = group
	for _, r := range rooms {
		rm, _ := r.(map[string]any)
		room := Room{Name: rm["name"].(string)}
		if p, ok := rm["proctor"].(string); ok {
			room.Proctor = p
		}
		for _, s := range rm["seats"].([]any) {
			sm, _ := s.(map[string]any)
			seat := Seat{Idx: int(sm["idx"].(float64))}
			seat.Reg, _ = sm["reg"].(string)
			seat.Name, _ = sm["name"].(string)
			seat.Class, _ = sm["class"].(string)
			room.Seats = append(room.Seats, seat)
		}
		plan.Rooms = append(plan.Rooms, room)
	}
	return plan
}
