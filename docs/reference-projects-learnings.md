# 参考项目对比学习报告（答题卡阅卷 / OMR）

> 对比对象：OpenGrade 本仓库 vs 4 个 GitHub 开源参考项目（已克隆至 `D:\omr-study\`，均为浅克隆）。
> 日期：2026-10-26。结论基于逐文件精读，关键结论均标注参考项目源码位置。

## 0. 参考项目速览

| 项目 | 星数/语言 | 定位 | 完成度 |
|---|---|---|---|
| Udayraj123/OMRChecker | 1187 / Python | 模板化 OMR 批量判读框架（ Technothlon 实战过） | 高（判读侧），无出卡、无业务系统 |
| iansan5653/open-mcr | 212 / Python | 桌面阅卷工具（75/150 题两张固定卡） | 中，工程规范好 |
| Wasim37/answer_card_detection | 147 / Python | 单张卡 demo：切分+考号+判读 | 低（demo 级，硬编码像素常量） |
| inuyasha2012/answer-sheet-scan | 169 / Python | 摄像头读卡 WIP | 极低（Python2、单帧、无输出层） |

**总体结论：判读内核的定位鲁棒性、几何契约（出卡↔读卡同源）、异常处理闭环、业务完整性，OpenGrade 全面领先；
真正的差距集中在三处——判读阈值的自适应统计、评分方案灵活性（漏选/倒扣/分节计分）、以及一批"锦上添花"的工程模式。**

---

## 1. OpenGrade 明显领先的部分（保持，不要被参考项目带偏）

1. **出卡↔读卡共享几何契约**。`card.Layout(mm) → Sheet(px@300DPI)` 编译后同时驱动出卡、判读、切片（`card/layout.go:589`）。4 个参考项目全都做不到：OMRChecker 无出卡代码；open-mcr 出卡是手绘 SVG、读卡几何硬编码在 `grid_info.py`（两套易漂移）；answer_card_detection 无任何模板；answer-sheet-scan 的 PDF 模板与 `settings.py` 参数互相矛盾。
2. **定位角标五级兜底阶梯 + 码区审计**（`omr/locate.go:37` + `omr/locate_rescue.go`）：连通域三重闸门 → 比例自校准 → 积分图多尺度实心窗匹配 → 位置吸附 → 相邻角标仿射/相似推算，外加"留一法"吊销被 QR/条码 finder 顶替的假角标、trimFrame 裁黑边。对比：OMRChecker 4 个 marker 任一失配整卷判死（`CropOnMarkers.py:101-131` 无兜底）；open-mcr 仿射基第一个命中的 L 形即用、倒页即失败、无重试；两个中文项目根本没有定位标检测。
3. **全 8 自由度单应矫正回 300DPI 标准网格**（`omr/homography.go` DLT+部分主元消元），分辨率无关、支持照片翻拍。open-mcr 只有 3 点仿射基（无透视矫正，`corner_finding.py:139`），中文两个项目生产路径甚至不做透视变换。
4. **考号三通道融合**：填涂 + Code128（13 线多数票 + mod-103 校验）+ 二维码复合码（考试短号+卷种位+考号），交叉校验、告警清理、AB 卷种点互核（`omr/regcode.go`、`omr/omr.go:138-185`）。所有参考项目均无条码/二维码通道。
5. **异常处理闭环**（服务端）：空白卡检测、偏浅涂写告警、多涂告警、考号漏涂→名册"可读位匹配"推荐、AB 卷种待指派、作废/重复卷裁决、缺考剔除、多页按页点归组。参考项目最接近的只是 OMRChecker 的两个 CSV（且其文件搬移是 TODO 桩 `entry.py:388-391`）。
6. **paperan 半自动版面分析**（`omr/paperan.go`）：双栏检测（分半各自矫斜再行列配对验证）、投影切行/切簇、封闭候选集字形模板（A–F、对/错、数字）、题号连续性审计补占位、主观留白区、题干截图入库。参考项目无任何等价物（answer_card_detection 用 OCR 驱动题号定位，思路可借鉴但依赖 tesseract/cnocr，不可离线复现、错误静默）。
7. **测试策略**：`synth` 模拟真实扫描损伤（透视+旋转+光照不均+噪声+JPEG）做端到端验证、paperan_stress_test 等。open-mcr 的 e2e 真实扫描 fixture 回归值得补（见下），OMRChecker 是 CSV 快照对比。

## 2. 参考项目做得更好、值得吸收的技术（按优先级）

### P0-1 逐题自适应判读阈值（来自 OMRChecker，最值得抄的算法）
**现状**：OpenGrade 用全局固定 `MarkThreshold=0.42`，仅在其下方 0.12 带内报"涂写偏浅"（`omr/omr.go:251-258`）。对同一张卡上的油墨深浅不均、铅笔压力差异、复印件，固定阈值要么漏判要么误报。
**参考做法**：OMRChecker 读的是每泡矩形灰度均值（`src/core.py:223-230`），然后两级统计：
- 全局阈值：把全卡所有泡均值排序，找"首个大跳变"（跳幅 > `MIN_JUMP=25`）取中点；复印件检测（打到 255 判"像复印件"）；
- 逐题（每行泡）局部阈值：行内排序找最大相邻跳变取中点，跳变不可信（<`MIN_JUMP+CONFIDENT_SURPLUS`）且行内 std 无离群时回退全局（`core.py:602-701`）。
**落地建议**：不必推翻 inkRatio（对背景噪声更稳），而是在判读前按题收集 `ChoiceRatio[qi][oi]`，对每题做"行内跳变中点"自适应阈值，与全局 0.42 融合（例如 `th_q = clamp(全局, 行内估计±0.15)`），并把阈值差作为置信度输出，替代现在硬编码的 0.12 偏浅带。注意 open-mcr 的教训（`grid_reading.py:370-403`）：按"涂点占比小的假设"取前 1/5 分位找 gap，空白卡/全涂卡会退化——必须带白卡/全涂 sanity 夹持。

### P0-2 评分方案：漏选部分分、多解加权、倒扣、分节计分（来自 OMRChecker）
**现状**：OpenGrade 客观题是精确串匹配全对才得分（`score/score.go:308`），多选题没有"漏选得部分分"，也没有倒扣/不答计分配置。
**参考做法**：`evaluation.py:42-143,531-546` 定义 `DEFAULT` 计分方案（correct/incorrect/unmarked 三个分值，可以是分数 `"-1/2"`）+ 命名 section 按题号区间套用不同方案 + 三种答案类型：单解、多解任一命中、`multiple-correct-weighted`（每解带权重分）。
**落地建议**：给 Layout/考试加"评分方案"对象：`correct / incorrect / unmarked / multiPartial / multiWrongAny`（多选：漏选按比例、错选判 0 或倒扣），分节（如 1-10 判断、11-40 单选）可各自覆盖。这是阅卷系统的常见刚需，国内市场"漏选得一半分"尤其常见。

### P1-1 可配置预处理链（来自 OMRChecker 插件架构）
**现状**：OpenGrade 只有 `Threshold: otsu|adaptive` 两档 + 定位兜底里的 inkPercentile。
**参考做法**：模板 JSON 声明有序 preprocessor 链（`Levels` gamma/levels LUT、`GaussianBlur`、`MedianBlur`、`CropPage`、`CropOnMarkers`、`FeatureBasedAlignment`），JSON Schema 校验、按目录配置（`src/processors/`、`template_schema.py:64-184`）。
**落地建议**：给 Options 加最小集：`gamma`（阴影/褪色件提亮）、`medianBlur`（椒盐噪声）、`clipLimit` CLAHE。实现成本极低（都是 LUT/卷积），收益直接——OMRChecker 对复印件/弱光件的容忍主要靠这层。ORB 特征对齐参考图那条可以不抄（依赖参考扫描件，运维成本高）。

### P1-2 矫正后逐块平移自对齐（印刷漂移防御，来自 OMRChecker auto_align）
**现状**：单应只建模四角标定义的全局几何；同一版面印刷时整体偏移/逐行走位（打印漂移）在矫正后仍会残留，表现为边缘题判读抖动。参考项目里 answer-sheet-scan 的滑动窗口实测网格（`utils.py:213-267`，"量出来而不是配出来"）是同一问题的另一解法。
**参考做法**：OMRChecker `core.py:97-199`：对每个 fieldBlock，用 5px 条带在块左侧/右侧滑窗找墨，左右平移（步长 1px，≤20 步）直到条带不再有墨，把 shift 加到每个泡 x。局限：只修正 x、逐块平移不修正斜——OpenGrade 可做得更好：在矫正图上检测该题行实际打印的泡圆心（小连通域网格均值），与理论位置求残差均值/中位数，超 0.3×泡半径即整体 snap，并把残差写进 Warnings 供审计。
**落地建议**：作为"判读前自检"实现：先按理论几何取每个泡外环（1.0r~1.4r 环带）看是否有印刷圆环墨迹，行/列方向求平均偏移，整体平移修正。这对"打印机边距差异/复印缩放局部失真"是实打实的准确率提升。

### P1-3 答案卡读答案（key sheet，来自 open-mcr）
**参考做法**：约定学号 `9999999999` 的是教师涂的标准答案卡，走同一条 OMR 管线读出 key（`grid_info.py:7`、`process_input.py:116-124`）；也支持 CSV 导入。
**落地建议**：OpenGrade 已有完整 OMR 管线与"批量设分值"，加一个 `ogctl key -layout ... -image teacher-key.jpg` / 考试详情页"扫答案卡录答案"入口即可，几乎零新代码。对"只有纸质答案、老师不想手动录 60 题"的场景体验极好。注意 open-mcr 的坑：key 卡有一位判错就静默变学生卡——需加"key 卡必须 RegNumber 精确命中约定值，否则进异常"的硬校验。

### P2-1 同题乱序变式的重排计分（arrangement map，来自 open-mcr）
`data_exporting.py:173-220`：一张 key + "题号→新位置"映射 CSV，把乱序卷结果重排后计分。OpenGrade 的 AB 卷是两套版式两套 key；可扩展为"N 套版式 + 1 张 key + 排列表"，省去逐卷维护多份答案。

### P2-2 逐卷判读叠加图落盘（来自 OMRChecker CheckedOMRs）
OpenGrade 有 `omr.RenderDebugOverlay`（绿圈已涂/黄圈偏浅）但仅 ogctl `-debug-dir` 手动导出。OMRChecker 每卷自动存 `CheckedOMRs/<file_id>` 标注图 + 1~6 级中间产物栈（`entry.py:322-334`、`core.py:703-726`）。建议：服务端判读时可选落盘叠加图（异常卷必存、正常卷抽样存），复核页直接看"机器看到了什么"，处理争议极快。

### P2-3 真实扫描 e2e 回归集（来自 open-mcr）
open-mcr 用 5 组真实扫描 + 期望 CSV 做字节级回归（`test/end-to-end/`）。OpenGrade 的 synth 很强但终归是合成分布；建议攒 10~20 张真实异常扫描（订书钉遮挡、边缘裁切、复印件、手机斜拍）做成 `testdata/` 回归集，防算法回归。

### P2-4 每题评分解释表（来自 OMRChecker should_explain_scoring）
逐题 verdict/分值/得分明细 CSV（`evaluation.py:364-378`）。OpenGrade 有讲评课件与成绩册，但"单卷逐题为什么得这个分"的解释表对家长/学生申诉场景有用，导出成本低。

## 3. 参考项目的坑（明确不抄）

- OMRChecker：泡判读用灰度均值而非墨迹占比（`core.py:223-230`），纸面噪声/划线/X 形涂写会污染均值（其 detectCross 是未实现桩）；auto_align 只修 x 且已弃用（README:254）；文件搬移/统计是桩；结果文件按 12 小时制命名会跨天碰撞；无并行、无断点续跑。
- open-mcr：仿射基不抗透视；首个 L 形命中即用（`corner_finding.py:217` TODO）；动态阈值空白页退化；计分精确串匹配；单线程。
- answer_card_detection：像素级硬编码常量（宽>1200px、y>60、kernel 5/9/11），换 DPI 即失效；生产路径放弃透视矫正；OCR 依赖（pytesseract+cnocr）重且静默出错；demo 主函数指向不存在的图片。
- answer-sheet-scan：Python2、单帧决策、阈值硬编码 `0.8`（settings 常量未被引用）、无输出层、模板与配置互相矛盾——只有"无定位标、靠轮廓网格实测"的思路可参考，工程不可用。

## 4. OpenGrade 缺失功能清单（合并汇总）

> **吸收状态（2026-10-28 更新）**：10 项全部落地或已由等价机制覆盖，逐批提交记录见 git log
> （51a387a / 49e584a / 78836ee / 9ef85fb 及收尾批）。

| # | 功能 | 来源 | 优先级 | 状态 |
|---|---|---|---|---|
| 1 | 逐题/逐卷自适应判读阈值 + 置信度输出 | OMRChecker / open-mcr | P0 | ✅ `omr/adaptive.go` 行内跳变统计+全局融合（只向下放宽，干净卡零回归）|
| 2 | 评分方案：多选漏选部分分、多解加权、倒扣、不答计分、分节方案 | OMRChecker | P0 | ✅ `card/scoring.go` + `score/evaluate.go` 全系统单一判分口径 |
| 3 | gamma/CLAHE/中值滤波预处理选项 | OMRChecker | P1 | ✅ `raster/improc.go` + `omr.Preprocess`（随考试持久化）|
| 4 | 矫正后印刷漂移自对齐（泡网格残差检测+snap） | OMRChecker auto_align 思路改良 | P1 | ✅ `omr/align.go` 泡圈连通域匹配（比环带质心抗文字/条码污染）|
| 5 | 答案卡扫读录答案（key sheet） | open-mcr | P1 | ✅ 考号全 9 硬校验 + 冲突不覆盖；`ogctl key` |
| 6 | 乱序变式重排计分（1 key + arrangement map） | open-mcr | P2 | ✅ 等价已存在：AB 卷 `OrderB` 排列表 + 共用 `ObjectiveKey`，B 卷作答入库即归一题序；N>2 变体受卡面卷种点(2 点)约束，暂不扩展 |
| 7 | 逐卷判读叠加图自动落盘（异常卷必存） | OMRChecker | P2 | ✅ 异常卷必存 + 1/20 抽样，复核页直看"机器视角" |
| 8 | 真实异常扫描 e2e 回归集 | open-mcr | P2 | ✅ 脚手架就绪（`omr/testdata/realscan/` + 指引 docs/REALSCAN.md），样本随真实扫描件积累 |
| 9 | 单卷逐题得分解释表导出 | OMRChecker | P3 | ✅ `GET /api/v1/exams/{id}/explain.xlsx` |
| 10 | 批量离线目录协议（目录继承模板，ogctl 补强） | OMRChecker | P3 | ✅ `ogctl read -image <目录>`，ogctl.json 逐级向上继承 |
