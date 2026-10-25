package omr

import (
	"bytes"
	"image/jpeg"
	"os"
	"testing"
)

// TestWriteFixture 一次性夹具导出(冒烟用,跑完即删):合成试卷 → JPEG。
func TestWriteFixture(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, renderTestPaper(t), &jpeg.Options{Quality: 88}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(`D:\gotmp\paper-fixture.jpg`, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
