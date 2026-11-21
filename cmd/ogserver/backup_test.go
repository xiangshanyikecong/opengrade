package main

// 备份目录外部化(本批):backupOnce 源/目标分离后的行为回归——
// 外部目录落盘、slices/pages/自指目录跳过、保留 N 份裁剪、目标不可写报错不 panic。

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writeBackupFixture(t *testing.T, src string) {
	t.Helper()
	write := func(rel, content string) {
		p := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("org.json", `{"users":{}}`)
	write("school.json", `{"grades":[]}`)
	write("ex0/exam.json", `{"id":"ex0"}`)
	write("ex0/slices/s01_q01.png", "not-json") // 图像不进备份
	write("ex0/pages/p1.png", "not-json")
}

func stampDirs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) == len("20060102-150405") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func TestBackupOnceExternalDir(t *testing.T) {
	src, dest := t.TempDir(), t.TempDir() // dest 模拟另一块物理盘
	writeBackupFixture(t, src)

	if err := backupOnce(src, dest, 10); err != nil {
		t.Fatalf("backupOnce: %v", err)
	}
	stamps := stampDirs(t, dest)
	if len(stamps) != 1 {
		t.Fatalf("外部目录应产生 1 个时间戳快照:%v", stamps)
	}
	for _, rel := range []string{"org.json", "school.json", "ex0/exam.json"} {
		if _, err := os.Stat(filepath.Join(dest, stamps[0], rel)); err != nil {
			t.Fatalf("快照缺 %s: %v", rel, err)
		}
	}
	// 图像目录不进备份
	if _, err := os.Stat(filepath.Join(dest, stamps[0], "ex0", "slices")); !os.IsNotExist(err) {
		t.Fatalf("slices 不应进备份:%v", err)
	}
}

func TestBackupOnceTrimAndSelfSkip(t *testing.T) {
	src := t.TempDir()
	dest := filepath.Join(src, "mybk") // 目标在源目录之内(自定义名,非默认 backups)
	writeBackupFixture(t, src)
	// 预置两个"历史快照"(超出 keep 的旧份)+ 一个目标目录内的 JSON(验证自指跳过)
	for _, old := range []string{"20200101-000000", "20200101-000001", "20200101-000002"} {
		p := filepath.Join(dest, old)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dest, "inner.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := backupOnce(src, dest, 2); err != nil {
		t.Fatalf("backupOnce: %v", err)
	}
	stamps := stampDirs(t, dest)
	if len(stamps) != 2 {
		t.Fatalf("keep=2 应只剩 2 份:%v", stamps)
	}
	if stamps[0] == "20200101-000000" {
		t.Fatalf("最旧的快照应被裁剪:%v", stamps)
	}
	// 自指目录内的 JSON 不应被快照(否则下次备份指数膨胀)
	for _, s := range stamps {
		if _, err := os.Stat(filepath.Join(dest, s, "inner.json")); !os.IsNotExist(err) {
			t.Fatalf("目标目录自身内容不应进备份:%v", err)
		}
	}
}

func TestBackupOnceUnwritableDest(t *testing.T) {
	src := t.TempDir()
	writeBackupFixture(t, src)
	block := filepath.Join(src, "blocker")
	if err := os.WriteFile(block, []byte("i am a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 目标路径撞文件:MkdirAll 失败 → 返回错误而非 panic
	if err := backupOnce(src, filepath.Join(block, "sub"), 2); err == nil {
		t.Fatal("目标不可写应返回错误")
	} else if !strings.Contains(err.Error(), "blocker") && !strings.Contains(err.Error(), "not a directory") &&
		!strings.Contains(err.Error(), "拒绝") && !strings.Contains(err.Error(), "denied") {
		// Windows/Unix 错误文案不同,只要求"有错且不 panic"
		t.Logf("目标不可写错误(可接受): %v", err)
	}
}
