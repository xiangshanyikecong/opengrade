package main

// 数据自动备份(P2):data/ 里的 JSON 状态文件(org.json/sessions.json 账号与会话、
// school.json 组织架构、题库、各考试 exam.json)定期快照,保留最近 keep 份。
// 目标目录可配(-backup-dir / OPENGRADE_BACKUP_DIR,缺省 data/backups)——建议指向
// data/ 之外的物理盘,数据盘损坏不连坐备份。
// ⚠ 只备份 JSON,不含 slices/ 整卷回溯图:原始扫描件并不落盘(入库即弃),
// 所以「从备份恢复=分数与作答在、答卷图像永久丢失」——恢复后无法重切片/重判卷,
// 只能重扫纸质卷。此为已知边界,不是可自动恢复的口径。
// 恢复:ogserver -restore-backup <备份时间戳目录> 把 JSON 拷回 data/(覆盖现状)。
// 启动即备份一次 + 每 12 小时一次;失败仅记日志,绝不影响主流程。
// JSON 写入均为原子落盘(temp+rename),备份协程拷不到"写了一半"的文件。

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// backupStampRe 匹配备份快照目录名(backupOnce 生成的 20060102-150405)。
var backupStampRe = regexp.MustCompile(`^\d{8}-\d{6}$`)

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
	// 裁剪:只保留最近 keep 份(按时间戳名排序,旧的后删)。
	// 只认本工具命名的时间戳目录(20060102-150405):-backup-dir 配到含其它数据的
	// 目录时,绝不能把别人的子目录当旧备份删掉(旧实现会按字典序删到只剩 keep 份)。
	entries, err := os.ReadDir(destRoot)
	if err == nil {
		var stamps []string
		for _, e := range entries {
			if e.IsDir() && backupStampRe.MatchString(e.Name()) {
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

// RestoreBackup 把备份快照目录里的 JSON 拷回数据目录(覆盖现状),返回恢复的文件数。
// 供 -restore-backup 启动参数使用(停服恢复);只拷 *.json,与备份口径一致。
func RestoreBackup(srcDir, dataDir string) (int, error) {
	if !backupStampRe.MatchString(filepath.Base(filepath.Clean(srcDir))) {
		return 0, fmt.Errorf("%s 不是备份快照目录(目录名应形如 20260101-030000)", srcDir)
	}
	n := 0
	err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(dataDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(path, dst); err != nil {
			return fmt.Errorf("恢复 %s: %w", rel, err)
		}
		n++
		return nil
	})
	if err != nil {
		return n, err
	}
	if n == 0 {
		return 0, fmt.Errorf("备份目录里没有可恢复的 JSON 文件")
	}
	return n, nil
}
