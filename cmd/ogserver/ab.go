package main

// AB 卷(版式 v3 增量):同一场考试 A/B 两套卡,防邻座抄袭。
//   - 启用方式两选一:建考 abAuto=1 由 A 卷版式自动派生 B 卷(客观题序打乱,见 deriveAB,
//     推荐路径);或高级手填 layout2/order2(B 卷题序自定,如倒序);
//   - 卡面卷种点:A 卡第 1 点实心、B 卡第 2 点实心(OMR 自动识别,与考号通道无关);
//   - 二维码/条码复合内容(个性化印刷卡):考试短号(4)+卷种位(1/2)+考号 —— 扫描时可核对
//     「这张卡是不是本场的」,并作为卷种点的冗余通道;
//   - B 卷可打乱客观题序:主观题区两版强制一致(建考校验),判读按卷种选版式,
//     作答在入库期按题序对照(OrderB)归一为规范题号 —— 统计/导出/趋势下游零改动;
//     判分按规范题号共用一份答案键(KeyB 恒等于 ObjectiveKey,入库即同步);
//   - 卷种判不出的卷:客观题作答暂扣(PendingVariant,不进统计),异常清单人工指派后
//     按所指派卷种从已存的矫正图重读恢复。

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/export"
	"github.com/opengrade/opengrade/omr"
)

// loadPageGroups 从 pages/ 目录装回某生的矫正图组(第 1 页 sNN.jpg,第 k>1 页 sNN_pKK.jpg;
// 卷种指派重读/跨批合并用)。无图返回空(如手工补录卷)。
// 页号可能跳号(缺页残卷按真实页号命名,如只有第 1、3 页):逐号探测到 60,不做
// "遇缺即停"——否则缺第 2 页的卷会把第 3 页也丢掉。返回按页号升序的图组。
func (s *Store) loadPageGroups(id string, student int) []*image.Gray {
	dir := s.PageDir(id)
	var out []*image.Gray
	appendIf := func(name string) bool {
		g, err := loadGrayJPEG(filepath.Join(dir, name))
		if err != nil {
			return false
		}
		out = append(out, g)
		return true
	}
	if !appendIf(fmt.Sprintf("s%02d.jpg", student)) {
		return nil
	}
	for k := 2; k <= 60; k++ {
		appendIf(fmt.Sprintf("s%02d_p%02d.jpg", student, k))
	}
	return out
}

func loadGrayJPEG(path string) (*image.Gray, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		return nil, err
	}
	if g, ok := img.(*image.Gray); ok {
		return g, nil
	}
	b := img.Bounds()
	g := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, gr, bb, _ := img.At(x, y).RGBA()
			g.SetGray(x-b.Min.X, y-b.Min.Y, color.Gray{Y: uint8((299*r + 587*gr + 114*bb) / 1000 >> 8)})
		}
	}
	return g, nil
}

// abSupport 是入库管线的 AB 上下文(nil / hasB=false = 非 AB 考试,全部走原路径)。
type abSupport struct {
	hasB    bool
	booksB  []*card.Sheet // B 版式 BuildBook
	orderB  []int         // 卡面第 i 题(1-based)→ 规范题号
	shortID string        // 本场考试短号(复合码核对)
}

// abOfExam 从考试会话构造 AB 上下文;非 AB 考试返回 nil。
func abOfExam(ex *Exam) (*abSupport, error) {
	if ex.LayoutB == nil {
		return nil, nil
	}
	bookB, err := ex.LayoutB.BuildBook()
	if err != nil {
		return nil, fmt.Errorf("B 卷版式编译失败: %w", err)
	}
	return &abSupport{hasB: true, booksB: bookB, orderB: ex.OrderB, shortID: ex.ShortID}, nil
}

// deriveAB 由 A 卷版式自动派生 B 卷(建考「启用 AB 卷」开关):
// 客观题顺序随机打乱并尽量取错位排列(无固定点 → B 卷题序必与 A 不同),其余
// (卷头/考号通道/分栏/主观题区)与 A 完全一致。返回 B 版式与题序对照
// (B 卡面第 i 题 → 规范题号)。调用方保证客观题数 ≥ 2。
func deriveAB(la *card.Layout, rng *rand.Rand) (*card.Layout, []int) {
	n := len(la.Questions)
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	perm := rng.Perm(n) // 0-based:B 卡面第 i 题 = 规范第 perm[i]+1 题
	for tries := 0; tries < 128; tries++ {
		fixed := false
		for i, v := range perm {
			if v == i {
				fixed = true
				break
			}
		}
		if !fixed {
			break
		}
		perm = rng.Perm(n) // 1/n 概率出现固定点,重抽(错位排列概率 ≈ 37%,128 次全败可忽略)
	}
	lb := *la
	lb.Variant = "B"
	qs := make([]card.QuestionSpec, n)
	orderB := make([]int, n)
	for i, src := range perm {
		qs[i] = la.Questions[src]
		orderB[i] = src + 1
	}
	lb.Questions = qs
	return &lb, orderB
}

// validateAbLayouts 建考校验 A/B 双版式:
//   - 主观题区必须完全一致(数量/题型/分值/小问/选做组 —— 切片与评分键共用);
//   - 客观题集合相同(题型/选项数/分值/知识点),仅顺序可不同;
//   - OrderB 必须是 1..N 的排列,且与 B 卡面实际题序一致(规格可区分的题逐一比对);
//   - KeyB 长度与客观题数一致。
//
// 返回归一化后的 KeyB(规范题号序)。
func validateAbLayouts(a, b *card.Layout, keyB []string, orderB []int) ([]string, error) {
	sheetA, err := a.Build()
	if err != nil {
		return nil, fmt.Errorf("A 卷版式: %w", err)
	}
	sheetB, err := b.Build()
	if err != nil {
		return nil, fmt.Errorf("B 卷版式: %w", err)
	}
	na, nb := sheetA.Layout, sheetB.Layout
	if len(na.Questions) != len(nb.Questions) {
		return nil, fmt.Errorf("AB 卷客观题数量不一致(A %d / B %d)", len(na.Questions), len(nb.Questions))
	}
	if len(keyB) != len(nb.Questions) {
		return nil, fmt.Errorf("B 卷参考答案 %d 道与客观题 %d 道不一致", len(keyB), len(nb.Questions))
	}
	if !reflect.DeepEqual(na.Subjectives, nb.Subjectives) {
		return nil, fmt.Errorf("AB 卷主观题区必须完全一致(题量/分值/小问/选做组),B 卷只能打乱客观题序")
	}
	key := func(q card.QuestionSpec) string {
		return strings.Join([]string{q.Kind, strconv.Itoa(q.Options), fmt.Sprint(q.Score), q.Knowledge, q.BankID}, "\x00")
	}
	ka := make([]string, len(na.Questions))
	kb := make([]string, len(nb.Questions))
	for i, q := range na.Questions {
		ka[i] = key(q)
	}
	for i, q := range nb.Questions {
		kb[i] = key(q)
	}
	sa, sb := append([]string{}, ka...), append([]string{}, kb...)
	sort.Strings(sa)
	sort.Strings(sb)
	for i := range sa {
		if sa[i] != sb[i] {
			return nil, fmt.Errorf("AB 卷客观题集合不一致(第 %d 个题规格不同):B 卷只能打乱题序,不能增删或改题型/分值", i+1)
		}
	}
	if len(orderB) != len(nb.Questions) {
		return nil, fmt.Errorf("B 卷题序对照 %d 项,应为 %d 项(卡面每题对应规范题号)", len(orderB), len(nb.Questions))
	}
	seen := map[int]bool{}
	for _, v := range orderB {
		if v < 1 || v > len(nb.Questions) || seen[v] {
			return nil, fmt.Errorf("B 卷题序对照必须是 1~%d 的排列(得到 %v)", len(nb.Questions), orderB)
		}
		seen[v] = true
	}
	// B 卡面题序与题序对照的一致性(规格可区分的题逐一比对):卡面第 i 题的规格必须与
	// 对照所指的规范题一致。规格完全相同的题互换无害(校验不出也不需要);规格可区分时
	// 版式与对照矛盾 = 客观题判分错位,必须在建考拦下。
	for i := range nb.Questions {
		if kb[i] != ka[orderB[i]-1] {
			return nil, fmt.Errorf("B 卷卡面第 %d 题与题序对照所指的 A 卷第 %d 题规格不一致(题型/选项数/分值/知识点不同):B 卷版式与题序对照矛盾", i+1, orderB[i])
		}
	}
	// KeyB(卡面序)→ 规范序:统分只用规范键,B 卷作答入库时同步归一
	canon := make([]string, len(keyB))
	for i, k := range keyB {
		canon[orderB[i]-1] = k
	}
	return canon, nil
}

// reorderAnswers 把卡面序作答按题序对照重排为规范题号序(B 卷入库归一)。
func reorderAnswers(answers []string, order []int) []string {
	out := make([]string, len(answers))
	for i, a := range answers {
		if i < len(order) && order[i] >= 1 && order[i] <= len(out) {
			out[order[i]-1] = a
		}
	}
	return out
}

// abFinalizeResult 对一份已判读的结果做 AB 后处理:
// 复合码考试短号核对 → 卷种缺失暂扣客观题(B 卷作答重排须在读页阶段完成,见 ingest)。
func abFinalizeResult(res *omr.Result, ab *abSupport) {
	if ab == nil || !ab.hasB {
		return
	}
	if res.CodeShort != "" && ab.shortID != "" && res.CodeShort != ab.shortID {
		res.Warnings = append(res.Warnings, fmt.Sprintf("码内考试短号 %s 与本场 %s 不符,扫描件疑似其它考试,请人工核对", res.CodeShort, ab.shortID))
	}
	if res.Variant == "" {
		res.PendingVariant = true
		res.Answers = nil // 客观题作答与卷种绑定,指派前不进统计也不给错分
		res.Warnings = append(res.Warnings, "卷种无法识别(卷种点未判出且码无卷种位),客观题暂不计分——请在异常清单指派 A/B 卷")
	}
}

// variantBook 返回某卷种第 k 页应使用的 Sheet(A 版式为缺省)。
func (ab *abSupport) variantBook(book []*card.Sheet, variant string, k int) *card.Sheet {
	if ab != nil && ab.hasB && variant == "B" && k < len(ab.booksB) {
		return ab.booksB[k]
	}
	if k < len(book) {
		return book[k]
	}
	return book[len(book)-1]
}

// handleAssignVariant 人工指派卷种:POST /api/v1/exams/{id}/assign-variant {"student":N,"variant":"A"|"B"}
// 权限同作废(建卷人/组长/管理员,留审计)。指派后按该卷种从已存矫正图重读客观题作答并归一,
// 清除暂扣标记;卷种本来就明确的卷也允许改派(改派即重读)。
func handleAssignVariant(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在或无权访问该考试(不在你的任教/管辖范围)"))
		return
	}
	if !canEditExam(u, ex) {
		httpError(w, http.StatusForbidden, fmt.Errorf("仅管理员/年级组长/建卷人可指派卷种"))
		return
	}
	var req struct {
		Student int    `json:"student"`
		Variant string `json:"variant"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if req.Variant != "A" && req.Variant != "B" {
		httpError(w, http.StatusBadRequest, fmt.Errorf("variant 须为 A 或 B"))
		return
	}
	if err := store.AssignVariant(ex.ID, req.Student, req.Variant, u.Username); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleExamCardsPDF AB 卷个性化出卡:GET /api/v1/exams/{id}/cards.pdf?variant=A|B
// 按名册逐生渲染(卷种点 + 复合码二维码:考试短号+卷种位+考号),多页版式逐页出。
func handleExamCardsPDF(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false)
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	variant := r.URL.Query().Get("variant")
	if variant != "A" && variant != "B" {
		httpError(w, http.StatusBadRequest, fmt.Errorf("variant 须为 A 或 B"))
		return
	}
	base := &ex.Layout
	if variant == "B" {
		if ex.LayoutB == nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("本场考试未配置 B 卷版式"))
			return
		}
		base = ex.LayoutB
	}
	book, err := base.BuildBook()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	if ex.ShortID == "" {
		httpError(w, http.StatusBadRequest, fmt.Errorf("本场考试没有考试短号(重建考试或补元数据)"))
		return
	}
	vd := byte('1')
	if variant == "B" {
		vd = '2'
	}
	prefix := ex.ShortID + string(vd)
	count := 0
	var pageImages []image.Image
	for _, st := range ex.Roster {
		if st.Reg == "" || len(st.Reg) != base.RegDigits {
			continue // 考号位数不符的学生跳过(名册脏数据),不阻断整包
		}
		digits := make([]int, len(st.Reg))
		bad := false
		for i, ch := range st.Reg {
			digits[i] = int(ch - '0')
			if digits[i] < 0 || digits[i] > 9 {
				bad = true
				break
			}
		}
		if bad {
			continue
		}
		f := &card.Filling{Reg: digits, CodePrefix: prefix}
		for _, sheet := range book {
			pageImages = append(pageImages, card.RenderPNG(sheet, f).ToGray())
			count++
		}
	}
	if count == 0 {
		httpError(w, http.StatusUnprocessableEntity, fmt.Errorf("名册中没有考号位数匹配的学生,无法出卡"))
		return
	}
	data, err := export.PagesPDF(pageImages, 210, 297)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=opengrade-cards-%s-%s.pdf", ex.ID, variant))
	_, _ = w.Write(data)
}
