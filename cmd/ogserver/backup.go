package main

// 数据自动备份(P2):data/ 里的 JSON 状态文件(org.json 账号库、school.json 组织架构、
// 题库、各考试 exam.json)定期快照,保留最近 keep 份。
// 目标目录可配(-backup-dir / OPENGRADE_BACKUP_DIR,缺省 data/backups)——建议指向
// data/ 之外的物理盘,数据盘损坏不连坐备份。只备份 JSON(体积小、信息全;切片/回溯图
// 可由原始扫描重生成,不进备份)。启动即备份一次 + 每 12 小时一次;失败仅记日志,绝不影响主流程。

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// StartBackupLoop 启动后台备份协程(非阻塞)。destDir 为空时备份到 srcDir/backups;
// 指定外部目录(如另一块物理盘)时,快照与裁剪都以该目录为根,数据目录损坏不再连坐备份。
func StartBackupLoop(srcDir, destDir string, interval time.Duration, keep int) {
	if destDir == "" {
		destDir = filepath.Join(srcDir, "backups")
	}
	go func() {
		for {
			if err := backupOnce(srcDir, destDir, keep); err != nil {
				log.Printf("数据备份失败(不影响服务;目标目录 %s 可写吗?): %v", destDir, err)
			}
			time.Sleep(interval)
		}
	}()
}

// backupOnce 执行一次备份并裁剪旧备份:源始终是 srcDir 下的 JSON,目标是 destRoot/<时间戳>/。
func backupOnce(srcDir, destRoot string, keep int) error {
	stamp := time.Now().Format("20060102-150405")
	dest := filepath.Join(destRoot, stamp)
	absDest, _ := filepath.Abs(destRoot)

	var files []string
	err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 读不了的跳过
		}
		if d.IsDir() {
			name := d.Name()
			if name == "backups" || name == "slices" || name == "pages" {
				return filepath.SkipDir // 备份目录防自噬;大体积图像不进备份
			}
			// 目标目录在源目录之内(用户把 -backup-dir 指到 data/ 下)时同样跳过,避免递归快照
			if abs, _ := filepath.Abs(path); abs == absDest {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".json") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	copied := 0
	for _, src := range files {
		rel, err := filepath.Rel(srcDir, src)
		if err != nil {
			continue
		}
		dst := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(src, dst); err != nil {
			return err
		}
		copied++
	}
	// 裁剪:只保留最近 keep 份(按时间戳名排序,旧的后删)
	entries, err := os.ReadDir(destRoot)
	if err == nil {
		var stamps []string
		for _, e := range entries {
			if e.IsDir() {
				stamps = append(stamps, e.Name())
			}
		}
		sort.Strings(stamps)
		for i := 0; i < len(stamps)-keep; i++ {
			_ = os.RemoveAll(filepath.Join(destRoot, stamps[i]))
		}
	}
	log.Printf("数据备份完成:%s(%d 个 JSON 文件)", dest, copied)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
