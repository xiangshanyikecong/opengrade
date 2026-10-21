package auth

import (
	"encoding/hex"
	"testing"
)

func TestPasswordHashRoundtrip(t *testing.T) {
	u := &User{Username: "t"}
	if err := u.SetPassword("secret1"); err != nil {
		t.Fatal(err)
	}
	if !u.CheckPassword("secret1") {
		t.Fatal("正确口令应通过校验")
	}
	if u.CheckPassword("secret2") {
		t.Fatal("错误口令不应通过")
	}
	if u.Salt == "" || u.Hash == "" || u.Salt == u.Hash {
		t.Fatal("盐值与散列应非空且不同")
	}
	// RFC 6070 风格的已知答案测试(小迭代,验证 PBKDF2 实现正确性)
	got := pbkdf2Key([]byte("password"), []byte("salt"), 1, 20)
	want := "120fb6cffcf8b32c43e7225256c4f837a86548c9"
	if hex.EncodeToString(got) != want {
		t.Fatalf("PBKDF2 已知答案不匹配: %x", got)
	}
	got2 := pbkdf2Key([]byte("password"), []byte("salt"), 2, 20)
	want2 := "ae4d0c95af6b46d32d0adff928f06dd02a303f8e"
	if hex.EncodeToString(got2) != want2 {
		t.Fatalf("PBKDF2(iter=2) 已知答案不匹配: %x", got2)
	}
}

func TestUserStoreLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Bootstrap()
	if err != nil || !first {
		t.Fatalf("首次 Bootstrap 应创建默认管理员: first=%v err=%v", first, err)
	}
	first2, _ := s.Bootstrap()
	if first2 {
		t.Fatal("第二次 Bootstrap 不应重复创建")
	}
	// 登录
	token, u, err := s.Login("admin", "admin123")
	if err != nil || u.Role != RoleAdmin {
		t.Fatalf("管理员登录失败: %v", err)
	}
	if got, ok := s.SessionUser(token); !ok || got.Username != "admin" {
		t.Fatal("会话应有效")
	}
	// 错误口令
	if _, _, err := s.Login("admin", "wrong"); err == nil {
		t.Fatal("错误口令应失败")
	}
	// 创建教师与学生
	if err := s.CreateUser(&User{Username: "t1", DisplayName: "张老师", Role: RoleTeacher, Class: "三年二班"}, "tt12345"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(&User{Username: "s1", DisplayName: "李雷", Role: RoleStudent, Reg: "00000001", Class: "三年二班"}, "ss12345"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(&User{Username: "x", DisplayName: "x", Role: RoleTeacher}, "123"); err == nil {
		t.Fatal("弱口令应被拒绝")
	}
	// 重开库:持久化验证
	s2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := s2.SessionUser(token); !ok || got.Username != "admin" {
		t.Log("会话为内存态,重启后需重新登录(预期)")
	}
	if u := s2.Users["t1"]; u == nil || !u.CheckPassword("tt12345") {
		t.Fatal("教师账号应已持久化且口令可校验")
	}
	// 保护最后一名管理员
	if err := s2.DeleteUser("admin"); err == nil {
		t.Fatal("不应能删除唯一管理员")
	}
	// 删除教师
	if err := s2.DeleteUser("t1"); err != nil {
		t.Fatal(err)
	}
	// List 视图类型上不存在散列字段(结构性保证不外泄)
	for _, x := range s2.List() {
		if x.Username == "" {
			t.Fatal("账号视图应含用户名")
		}
	}
}

func TestImportUpsert(t *testing.T) {
	s, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.ImportUpsert(&User{Username: "s1", DisplayName: "李雷", Role: RoleStudent, Reg: "00000001", Class: "三年二班"}, "pass123")
	if err != nil || !created {
		t.Fatalf("首次导入应创建: created=%v err=%v", created, err)
	}
	// 重复导入:仅更新行内给出的非空字段,不抹掉既有信息、不动口令
	created, err = s.ImportUpsert(&User{Username: "s1", Class: "三年三班"}, "")
	if err != nil || created {
		t.Fatalf("重复导入应为更新: created=%v err=%v", created, err)
	}
	u := s.Users["s1"]
	if u.Class != "三年三班" || u.DisplayName != "李雷" || u.Reg != "00000001" {
		t.Fatalf("更新语义错误: %+v", u)
	}
	if !u.CheckPassword("pass123") {
		t.Fatal("未提供密码时不应重置口令")
	}
	// 显式密码 → 重置
	if _, err := s.ImportUpsert(&User{Username: "s1"}, "newpass1"); err != nil {
		t.Fatal(err)
	}
	if !s.Users["s1"].CheckPassword("newpass1") {
		t.Fatal("显式密码应重置口令")
	}
	// 新账号无密码 → 报错
	if _, err := s.ImportUpsert(&User{Username: "s2", Role: RoleStudent}, ""); err == nil {
		t.Fatal("新账号缺密码应报错")
	}
	// 角色不合法 → 报错
	if _, err := s.ImportUpsert(&User{Username: "s3", Role: Role("boss")}, "pass123"); err == nil {
		t.Fatal("角色不合法应报错")
	}
}
