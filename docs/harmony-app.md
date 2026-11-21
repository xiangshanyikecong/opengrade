# OpenGrade 鸿蒙客户端(HarmonyOS NEXT)

> 仓库目录:`harmony/`。纯 ArkTS(Stage 模型),**零第三方 ohpm/npm 依赖**,仅使用系统 Kit。
> 目标:HarmonyOS NEXT(API 12+,compatibleSdkVersion `5.0.0(12)`),手机/平板。

## 一、架构

```
harmony/
├── AppScope/                    应用级配置(图标/名称)
├── build-profile.json5          工程构建配置(SDK 版本/模块表)
├── hvigorfile.ts / oh-package.json5
└── entry/src/main/
    ├── module.json5             权限:INTERNET / PUBLISH_AGENT_REMINDER / ACCESS_BIOMETRIC;
    │                            deviceTypes: phone/tablet
    ├── resources/base/profile/main_pages.json   路由页表(30 页)
    └── ets/
        ├── entryability/EntryAbility.ets        入口 UIAbility → pages/Index;
        │                                        深色跟随系统(onConfigurationUpdate)、
        │                                        退后台记应用锁意图
        ├── common/
        │   ├── OgNet.ets        HTTP 客户端:Bearer 注入、{error} 文案映射、401 全局登出、
        │   │                    multipart 上传(先拷沙箱)、下载另存为、
        │   │                    鉴权位图缓存(内存 LRU + 磁盘 filesDir/imgcache 持久层)
        │   ├── OgSession.ets    preferences 持久化:baseUrl/token/me + 多服务器 +
        │   │                    深色档位 + 通用 JSON 存取(已读标记/提醒 id)
        │   ├── OgTheme.ets      品牌色(#00B389 系,与 web/common.css 同源);
        │   │                    静态 getter 派发浅/深双调色板(auto/light/dark)
        │   ├── OgFormat.ets     分数/角色/题型/阶段文案(与 web 端同口径)
        │   ├── OgCache.ets      离线 JSON 缓存(filesDir/offline,学生端断网兜底)
        │   ├── OgShare.ets      系统分享(ShareKit;沙箱文件 fileUri → 分享面板)
        │   └── AppLock.ets      应用锁:本地 PIN(SHA-256)+ UserAuthKit 指纹/人脸(ATL1)
        ├── model/Api.ets        与 Go struct 逐字段对齐的 API 类型
        ├── components/
        │   ├── OgImage.ets      带鉴权图片(ArkUI Image 无法带 Authorization 头 → 下载解码 PixelMap)
        │   ├── InkCanvas.ets    归一化笔迹画板(契约同 score.Stroke:pts 0~1)
        │   ├── ScorePad.ets     触屏评分盘(≤10 单键即打;>10 十位/个位两键合成;±0.5;小数直录)
        │   ├── SliceStage.ets   切片舞台:图 + 笔迹叠加 + 灰度/反色 + 全屏细看(带笔迹进放大镜)
        │   ├── Charts.ets       Canvas 折线/柱状/横条/雷达(对齐 common.js chartLine 口径)
        │   ├── StaffNav.ets     教职员 ☰ 导航(全工作台页共用)
        │   └── Ui.ets           TitleBar/Panel/Error/Empty/Pill/KV/按钮 等轻量件
        └── pages/               30 个路由页(Index 分发/Lock/PdfViewer/ImageViewer +
                                 Login/ForcePwd/Me + student 4 页 + teacher 13 页 + admin 1 页)
```

## 二、鉴权:Bearer 通道(服务端配套,已实现)

原生 HTTP 客户端没有浏览器 Cookie 罐,服务端在**不改变 web 行为**的前提下增加双通道:

- `POST /api/v1/auth/login` 请求体可选 `"native": true` —— 仅此时响应附 `token` 字段
  (web 不带该字段,响应零变化,token 不暴露给页面脚本);
- `guard` 中间件:Cookie 优先,缺失回退 `Authorization: Bearer <token>`;
  令牌仍是 sessions.json 里的既有会话,7 天 TTL、改密/重置吊销、logout 吊销全部同语义;
- `handlePasswordChange` 的 keepToken 兼容 Bearer(改密保留当前设备会话);
- e2e:`cmd/ogserver/bearer_e2e_test.go` 覆盖 native 登录 → 三角色权限矩阵 → 吊销 →
  Cookie 令牌以 Bearer 复用。

客户端:`OgNet` 统一注入 Bearer;收到 401 时清会话并跳登录页(登录页自身不带 token,不会误触)。

## 三、页面与功能矩阵

| 模块 | 页面 | 覆盖能力 |
|---|---|---|
| 通用 | Index / Login / ForcePwd / Me | 服务器地址检测+多址记忆、native 登录、首登强制改密、改密、多服务器切换、登出、深色档位(auto/light/dark)、应用锁开关(PIN+生物) |
| 通用 | LockPage | 应用锁屏:PIN 数字键盘 + 指纹/人脸自动拉起;冷启动 Index 门禁 / 退后台回前台 EntryAbility 锁定 |
| 通用 | PdfViewerPage | PDF 逐页渲染(pdfService)+ 翻页 + 系统分享 + 另存为;学生成绩单/全班成绩条共用 |
| 学生/家长 | StudentHome(页签:成绩/趋势/我的) | 已发布考试列表(缺考/复核/**新成绩**角标)、家长多孩切换、断网离线兜底 |
| | StudentExamDetail | 总分卡(年级/班级排名/均分)、逐题对错(错题本过滤)、知识点掌握度(雷达图+横条)、主观切片、整卷回溯多页、复核申请提交与回复、错题重练卡入口、成绩单 PDF 预览/另存、断网离线兜底 |
| | StudentTrends | 按学科得分率曲线+班级对照线、逐场名次进退 |
| | Practice | 错题重练卡摘要 + 可打印 SVG 另存 |
| 教师 | Home(总览) | 聚合仪表盘 + 高频入口(含 待办中心/拍卷建考)+ 最近考试 |
| | TodoPage | 待办中心:阅卷任务/异常待裁决/⚑待复核/复核申请/待发布聚合,紧急置顶;任务截止本地提醒(提前 2 小时,reminderAgentManager 日历型,零云) |
| | TaskList | 我的任务(按题/按班/建卷人兜底、进度、截止、深链阅卷) |
| | ExamList | 阶段过滤/搜索、状态机主操作(去扫描/处理异常/阅卷/发布/成绩册) |
| | ExamHub | 阶段卡、逐题进度深链、异常清单裁决、重复卷保留、缺扫名单、作废恢复、⚑队列、复核回复+拉黑、元数据、答案修正重统、发布/撤回、导出下载 + **成绩条 PDF 预览**、删除 |
| | Grader | 切片预加载(后 8 张)、触屏评分盘、✓✗⚑、画笔批注(持久化)、小问逐问、选做涂选展示、匿名隐藏考号、409 拉取最新、自动跳转、待复核队列;**平板 ≥620vp 左卷右盘双栏**;切片全屏=放大镜(捏合缩放带笔迹) |
| | UploadScans | 相册多选/拍照/**本地 PDF 拆页(pdfService,2200px/页)** → 5 张分批接力、失败续传、跨批合并/告警反馈 |
| | LayoutScanPage | **拍卷建考**:拍照/相册 → 服务端版面识别 → 逐题确认(题型/选项/分值)→ 生成版式带入建考 / 批量导入题库(题干截图随题) |
| | CreateExam | 手工成绩免版式建考 / 扫描考试建档(拍卷识别带入 / 版式 JSON 文件导入+班级名册+AB 卷开关) |
| | ManualScores | 手工成绩逐格直录、缺考登记 |
| | Analytics | 统计摘要、分数段、逐题难度/区分度、知识点掌握度(**雷达图**+横条)、班级对比、五等/21 级赋分 |
| | StaffTrends | 班级均分走势、逐生跨考对齐与名次进退 |
| | Aggregate | 年级总分工表(横向滚动,缺考/无卷标记,可见科目口径提示) |
| | Assign | 按题/按班阅卷人分配 + 截止时间 |
| | Rooms | 考场编排:生成/座位对调/监考指派/冲突提示 |
| | BankList | 题库管理:列表/搜索/新建/编辑/删除/拍题干截图上传 |
| | Review | 学生个体复核页:逐题明细 + 改分 + 整卷回溯 |
| | Workload | 阅卷工作量统计 |
| 管理 | UserList | 账号列表/搜索、新建(全角色字段)、重置密码、删除 |

**留在桌面 Web**(App 内提供文案提示):设计器出卡、组织架构与场次表维护、CSV 批量导入。

## 四、关键实现决策

1. **图片鉴权**:ArkUI `Image` 不能附带请求头 → 统一走 `OgImage`/`ImageCache`:
   `OgNet.getBytes`(带 Bearer)→ `image.createImageSource` 解码(可选降采样)→ 内存 LRU(80 张)+
   **磁盘持久层**(`filesDir/imgcache`,LRU 400 张;取数顺序 内存→磁盘→网络,网络回写磁盘;
   「清理缓存」显式清空,离线/弱网切片可复用)。Grader 预加载直接 `ImageCache.prefetch`。
2. **笔迹契约**:`Stroke{color, pts[]}`,pts 为相对切片显示区归一化坐标(0~1),与 web 端完全同源;批注/评分同一次 `POST /grades` 持久化。
3. **评分乐观锁**:所有 `POST /grades` 携带读到的 `ver`;409 时 toast 并重拉本题保持卷位(与 web 同语义)。
4. **小问(版式 v3)**:打分写 `part` 维度(逐问独立 ver);mark/ink 变更按 web 语义同步写每个小问。
5. **上传**:picker 返回的 `file://media` URI 先 `fs.copyFile` 进沙箱缓存(multipart `filePath` 要求真实路径),5 张/批接力,失败即停可续传(与 web 的 15 张/批语义一致、批更小)。
6. **PDF 拆页/预览**:`@kit.PDFKit` pdfService —— 拆页:`loadDocument` → 逐页 `getAreaPixelMap`(整页 matrix,长边 2200px≈265dpi,保二维码考号模块可读)→ `packToData` JPEG 入上传队列;预览:同管线按宽 1240px 渲染 + 翻页。加密 PDF 拒绝;`PdfMatrix.rotate` 取 0(真机如报错回读文档)。
7. **下载交付**:`OgNet.downloadToPicker` = 下载到内存(DocumentViewPicker.save)→ 写入用户选择的位置;成绩册/数据包/成绩单/练习卡均走此路径;**分享**:`OgShare`(ShareKit,沙箱文件 `fileUri.getUriFromPath` → 系统分享面板,接收方临时授权)。
8. **强制改密**:登录响应 `mustChangePassword` → ForcePwdPage;服务端闸门(403)兜底。
9. **多服务器**:token 按服务器隔离,切换即登出;服务器列表存 preferences(最多 6 条)。
10. **深色模式**:颜色不再走常量而经 `OgC` 静态 getter 派发(浅/深双调色板);`auto`=跟随系统
    (EntryAbility `onConfigurationUpdate` 同步),Me 页可切 auto/light/dark(存 preferences)。
    驻留页面的配色在下次进入时刷新(Canvas 图表随重绘取新色)。
11. **应用锁**:PIN 4~6 位 SHA-256 后存本机 preferences;生物识别走 UserAuthKit(ATL1,指纹/人脸,
    `ACCESS_BIOMETRIC`);锁定时机 = 冷启动(Index 门禁)+ 退后台→回前台(EntryAbility 记单次锁定意图,
    250ms 延时拉起 LockPage,与 Index 门禁 takeLockIntent 互斥防双重路由)。全部本地校验,零上传。
12. **待办提醒**:reminderAgentManager 日历型本地提醒(截止前 2 小时),需 `PUBLISH_AGENT_REMINDER`
    (声明即得)+ `requestEnableNotification` 用户授权;重复设置先撤旧(记 id 列表)再发新;无任何云推送。
13. **离线兜底**:学生端成绩列表/详情请求失败时回退 `OgCache` 上次成功 JSON(带「离线数据」横幅);
    图片走磁盘缓存;「新成绩」角标用本机已读集合(进详情即消),纯本地不依赖服务端已读状态。
14. **拍卷建考**:服务端 `/api/v1/layout/analyze` 无状态返回题型构成 + 题干截图(base64)+ 叠加标注图;
    客户端逐页累计草稿(可多页),人工确认后拼 v2 版式 JSON(`{version:2,title,regDigits,regMode:'fill',
    columns,questions,subjectives}`)—— 与 Web 设计器 applyPaperDraft 同构;导题库逐题 POST /bank +
    stem 截图随传(部分失败不回滚)。

## 五、构建与真机验证(需 DevEco Studio 5.0+)

> 完整的模拟器/真机运行步骤、服务器连通排查与角色验收清单见 [harmony-emulator.md](harmony-emulator.md)。

```bash
# 服务端(先起,手机与电脑同一局域网)
go build -o ogserver ./cmd/ogserver
./ogserver -addr :8787 -seed-demo

# 客户端
# 1. DevEco Studio 打开 harmony/ 目录,等待 Sync 完成(零第三方依赖,无需 ohpm install 远程包)
# 2. File → Project Structure → Signing Configs:勾选 Automatically generate certificate(需登录华为账号)
# 3. 选择真机/模拟器,Run 'entry'
# 4. App 首页填入服务器地址(如 http://192.168.x.x:8787)→ 检测 → 登录
```

测试账号(`-seed-demo`):`admin/admin123`、`leader1/leader123`、`teacher1/teach123`(数学)、`s0001/student123`(学生,考号 00000001)。

真机冒烟清单:
- 学生:登录 → 成绩列表 → 详情逐题/切片/整卷 → 趋势 → 复核申请 → 练习卡保存;
- 家长:双孩切换;
- 教师:建考(手工)→ 拍照上传扫描 → 异常裁决 → 触屏阅卷(打分/笔迹/小问/409 双开)→ 发布 → 学情/总表/导出;
- 管理:用户新建/重置密码;
- 回归:web 端同账号操作互不干扰(双改 409)。

## 六、已知边界与后续项

- pdfService 拆页/预览、生物识别解锁、系统分享面板:已实现,**待真机验证**(依赖 DevEco 环境与设备;
  若 `getAreaPixelMap` 对 `rotate:0` 报错需回读 PDF Kit 文档调整);
- 深色模式:驻留页配色下次进入刷新(静态 getter 方案的已知取舍);Canvas 图表内嵌色随重绘生效;
- 拍卷识别适用 A4 单栏印刷体 ±5°,双栏/手写被服务端拒绝(半自动契约,确认页兜底);
- 推送通知:本地提醒(reminderAgentManager)已覆盖任务截止场景;需要云推送的场景与
  "数据 100% 本地化"定位冲突,暂不做;
- 上架:企业内部分发(hdc 安装/企业签名)或 AppGallery(需软著与教育类资质)。
