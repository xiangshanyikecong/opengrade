// gen 随机生成学生测试数据,产出两份可直接导入的 CSV:
//   - 名册 CSV(考号,姓名,班级)—— 建考试时随考试上传,对应 roster.Load 的格式
//   - 账号导入 CSV(用户名,姓名,角色,密码,考号,班级)—— 管理员批量建学生账号,
//     对应 cmd/ogserver/userimport.go 的固定列序
//
// 用法(仓库根目录): go run testdata/gen/main.go [-n 300] [-class-size 50] [-out testdata]
// 固定随机种子,输出可复现;文件为 UTF-8 带 BOM,Excel 直接打开不乱码。
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"github.com/opengrade/opengrade/roster"
)

// 常见姓氏,高频姓按大致人口占比重复出现,让生成结果更贴近真实分布
var surnames = strings.Fields(
	"王 王 王 王 王 王 李 李 李 李 李 张 张 张 张 张 刘 刘 刘 刘 陈 陈 陈 陈 杨 杨 杨 黄 黄 黄 " +
		"赵 赵 赵 吴 吴 吴 周 周 周 徐 徐 徐 孙 孙 马 马 朱 朱 胡 胡 郭 何 高 高 林 林 罗 郑 郑 梁 " +
		"谢 宋 唐 许 韩 冯 邓 曹 彭 曾 肖 田 董 潘 袁 蔡 蒋 余 杜 叶 程 苏 魏 吕 丁 任 卢 姚 沈 " +
		"钟 姜 崔 谭 陆 范 汪 廖 石 金 韦 贾 夏 付 方 邹 熊 白 孟 秦 邱 侯 江 尹 薛 闫 段 雷 龙 " +
		"黎 史 陶 贺 毛 郝 顾 龚 邵 万 钱 严 覃 武 戴 莫 孔 向 汤")

// 双字名(口语里真实常见的组合,避免逐字拼出的怪名)
var given2 = strings.Fields(
	"子轩 梓涵 浩然 宇轩 子涵 雨欣 俊杰 梓萱 若曦 思远 明轩 佳怡 梦洁 欣怡 雅静 海燕 志强 建国 建军 " +
		"文博 天翔 家豪 金凤 春兰 秋菊 冬梅 志明 桂英 秀兰 秀英 国华 文静 静怡 晓东 晓燕 玉梅 凤英 丽华 " +
		"春梅 德福 长贵 安邦 永强 卫东 红梅 玉兰 素芬 芳华 淑华 慧敏 佳琪 心怡 悦然 泽宇 昊天 子墨 一鸣 " +
		"鸿飞 皓轩 亦可 沐宸 沐阳 奕辰 宇泽 浩宇 铭轩 思彤 雨桐 诗涵 语嫣 梦琪 紫萱 雅琪 淑仪 志伟 家辉 " +
		"嘉豪 泽楷 曼玉 丽娟 敏娟 雅婷 志豪 宇豪 立辉 向阳 红兵 少华 少杰 世杰 铁柱 有财 来福 兴旺 广志 " +
		"学文 学武 文轩 博文 博雅 天佑 嘉懿 煜城 苑博 鑫鹏 晋鹏 伟祺 荣轩 越泽 烨磊 晟睿 天磊 炎彬 致远 " +
		"睿渊 明杰 立诚 立轩 立霖 春霞 桂芳 秀珍 秀云 素珍 素英 美玲 美凤 丽丽 亚男 亚楠 冬雪 金龙 玉龙 " +
		"志远 志刚 兴华 继红 继业 光明 正光 正阳 承志 承宇 明辉 晨曦 静文 静娴 婉婷 婉如 慧娟 巧云 巧珍 " +
		"书豪 书瑶 书涵 宇晨 宇航 雨桐 若汐")

// 单字名
var given1 = strings.Fields(
	"伟 芳 娜 敏 静 丽 磊 军 洋 勇 艳 杰 娟 涛 超 霞 平 刚 辉 鹏 华 健 斌 飞 宇 晨 曦 悦 欣 怡 " +
		"浩 诺 舟 帆 亮 雪 晶 云 峰 丹 婷 璐 倩 琳 鑫 龙 兰 玲 燕 慧 美 玉 蕾 楠 凯 恒 波 强 晴 " +
		"阳 童 遥 妍 睿 铭 骁 翔")

func uniq(fields []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	n := flag.Int("n", 300, "学生人数")
	classSize := flag.Int("class-size", 50, "每班人数")
	outDir := flag.String("out", "testdata", "输出目录")
	flag.Parse()

	// 固定种子:同一命令重复执行得到完全相同的名单,便于测试复现
	rng := rand.New(rand.NewSource(20261024))
	sur, g2, g1 := uniq(surnames), uniq(given2), uniq(given1)
	used := map[string]bool{}
	randName := func() string {
		for {
			name := sur[rng.Intn(len(sur))]
			if rng.Intn(100) < 80 { // 双字名为主
				name += g2[rng.Intn(len(g2))]
			} else {
				name += g1[rng.Intn(len(g1))]
			}
			if !used[name] {
				used[name] = true
				return name
			}
		}
	}

	classes := (*n + *classSize - 1) / *classSize
	type stu struct{ reg, name, class string }
	students := make([]stu, 0, *n)
	for c := 1; c <= classes; c++ {
		size := *classSize
		if left := *n - len(students); left < size {
			size = left
		}
		class := fmt.Sprintf("高一(%d)班", c)
		for s := 1; s <= size; s++ {
			// 考号 8 位 = 入学年份 4 位 + 班号 2 位 + 序号 2 位,与版式默认 RegDigits=8 对齐
			students = append(students, stu{
				reg:   fmt.Sprintf("2026%02d%02d", c, s),
				name:  randName(),
				class: class,
			})
		}
	}

	var b strings.Builder
	b.WriteString("考号,姓名,班级\n")
	for _, s := range students {
		fmt.Fprintf(&b, "%s,%s,%s\n", s.reg, s.name, s.class)
	}
	rosterCSV := []byte(b.String())

	// 密码列留空,导入时由 defaultPassword 统一设置
	b.Reset()
	b.WriteString("用户名,姓名,角色,密码,考号,班级\n")
	for _, s := range students {
		fmt.Fprintf(&b, "%s,%s,学生,,%s,%s\n", s.reg, s.name, s.reg, s.class)
	}
	usersCSV := []byte(b.String())

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal("创建输出目录失败: %v", err)
	}
	rosterPath := filepath.Join(*outDir, fmt.Sprintf("students-%d-roster.csv", len(students)))
	usersPath := filepath.Join(*outDir, fmt.Sprintf("students-%d-users.csv", len(students)))
	// BOM 让 Excel 直接打开不乱码;roster.Load 首列因 BOM 非纯数字而按表头跳过,不受影响
	bom := []byte("\xef\xbb\xbf")
	if err := os.WriteFile(rosterPath, append(append([]byte{}, bom...), rosterCSV...), 0o644); err != nil {
		fatal("写名册失败: %v", err)
	}
	if err := os.WriteFile(usersPath, append(append([]byte{}, bom...), usersCSV...), 0o644); err != nil {
		fatal("写账号 CSV 失败: %v", err)
	}

	// 用仓库的真实解析器验收:磁盘上的文件(含 BOM)必须能被 roster.Load 原样吃下
	onDisk, err := os.ReadFile(rosterPath)
	if err != nil {
		fatal("回读名册失败: %v", err)
	}
	loaded, err := roster.Load(onDisk)
	if err != nil {
		fatal("roster.Load 验收失败: %v", err)
	}
	if len(loaded) != len(students) {
		fatal("roster.Load 解析出 %d 条,期望 %d 条", len(loaded), len(students))
	}

	dist := map[string]int{}
	for _, s := range loaded {
		dist[s.Class]++
	}
	fmt.Printf("已生成 %d 名学生(固定种子 20261024,结果可复现),%d 个班:", len(loaded), classes)
	for c := 1; c <= classes; c++ {
		fmt.Printf(" 高一(%d)班 %d 人", c, dist[fmt.Sprintf("高一(%d)班", c)])
	}
	fmt.Println()
	fmt.Printf("名册: %s\n账号: %s\n", rosterPath, usersPath)
	fmt.Println("名册预览:")
	for i, s := range loaded {
		if i == 5 {
			break
		}
		fmt.Printf("  %s,%s,%s\n", s.Reg, s.Name, s.Class)
	}
}
