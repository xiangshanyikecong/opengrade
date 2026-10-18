// ogctl 是 OpenGrade 开源内核的命令行工具:
// 生成答题卡 → 识别扫描件 → 统分,以及一条龙端到端演示。
package main

import (
	"fmt"
	"os"
)

const usage = `ogctl — OpenGrade 开源答题卡工具(纯 Go,零第三方依赖)

用法:
  ogctl gen    生成答题卡(可选:模拟涂卡学生卷;支持 -layout-file 传入 v2 版式)
  ogctl read   识别答题卡扫描件(PNG/JPEG,支持旋转/透视/光照不均)
  ogctl slice  从扫描件裁出主观题切片(阅卷工作台的数据源)
  ogctl score  按参考答案统分,输出逐题得分率
  ogctl demo   端到端演示:出卡 → 模拟扫描 → OMR → 切片 → 阅卷 → 统分 → 讲评建议

示例:
  ogctl demo -o demo_output -students 5
  ogctl gen  -o og_out -students 3
  ogctl gen  -o og_out -layout-file my-layout.json -students 30
  ogctl read -layout og_out/sheet.layout.json -image scan.jpg -out r.json -threshold adaptive
  ogctl slice -layout og_out/sheet.layout.json -image scan.jpg -o slices/
  ogctl score -key og_out/key.json -results "r*.json"
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "gen":
		err = cmdGen(os.Args[2:])
	case "read":
		err = cmdRead(os.Args[2:])
	case "slice":
		err = cmdSlice(os.Args[2:])
	case "score":
		err = cmdScore(os.Args[2:])
	case "demo":
		err = cmdDemo(os.Args[2:])
	default:
		fmt.Print(usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}
