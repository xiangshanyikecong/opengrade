package roster

import "testing"

// 名册 CSV 首部空行不再让表头识别失效(修复前:空行占掉第 0 行,真表头被当数据,
// 整份名册因"考号必须为纯数字"被拒)。
func TestAuditLoadLeadingBlankLineBeforeHeader(t *testing.T) {
	csv := "\n\nreg,name,class\n0001,甲,一班\n0002,乙,一班\n"
	st, err := Load([]byte(csv))
	if err != nil {
		t.Fatalf("首部空行 + 表头应正常解析: %v", err)
	}
	if len(st) != 2 || st[0].Reg != "0001" || st[1].Name != "乙" {
		t.Fatalf("解析结果不符: %+v", st)
	}
	// 无表头(首行即数据)仍兼容;表头在首行(无空行)不受影响
	if st, err := Load([]byte("0003,丙\n")); err != nil || len(st) != 1 {
		t.Fatalf("无表头解析: %v %+v", err, st)
	}
	if st, err := Load([]byte("考号,姓名\n0004,丁\n")); err != nil || len(st) != 1 {
		t.Fatalf("常规表头解析: %v %+v", err, st)
	}
}
