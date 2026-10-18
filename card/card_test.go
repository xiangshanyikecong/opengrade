package card

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildCapacityError(t *testing.T) {
	l := Layout{Version: 1, Title: "T", RegDigits: 8, ChoiceCount: 62, Options: 4, Columns: 2}
	if _, err := l.Build(); err == nil {
		t.Fatal("62 题 2 栏(31 行 > 30 行)应超出单页容量而报错")
	}
}

func TestSVGElementCounts(t *testing.T) {
	l := Layout{Version: 1, Title: "TEST SHEET", RegDigits: 6, ChoiceCount: 20, Options: 4, Columns: 2}
	sheet, err := l.Build()
	if err != nil {
		t.Fatal(err)
	}
	svg := RenderSVG(sheet, nil)
	got := bytes.Count(svg, []byte("<circle"))
	want := 20*4 + 6*10
	if got != want {
		t.Fatalf("SVG 涂卡圆数量 = %d, 期望 %d", got, want)
	}
	if !strings.Contains(string(svg), "TEST SHEET") {
		t.Fatal("SVG 应包含卷头标题")
	}
}
