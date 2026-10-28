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
    ├── module.json5             权限仅 ohos.permission.INTERNET;deviceTypes: phone/tablet
    ├── resources/base/profile/main_pages.json   路由页表
    └── ets/
        ├── entryability/EntryAbility.ets        入口 UIAbility → pages/Index
        ├── common/
        │   ├── OgNet.ets        HTTP 客户端:Bearer 注入、{error} 文案映射、401 全局登出、
        │   │                    multipart 上传(先拷沙箱)、二进制下载+另存为、鉴权位图缓存(预加载共用)
        │   ├── OgSession.ets    preferences 持久化:baseUrl/token/me;多服务器列表
        │   ├── OgTheme.ets      品牌色(#00B389 系,与 web/common.css 同源)
        │   └── OgFormat.ets     分数/角色/题型/阶段文案(与 web 端同口径)
        ├── model/Api.ets        与 Go struct 逐字段对齐的 API 类型
        ├── components/
        │   ├── OgImage.ets      带鉴权图片(ArkUI Image 无法带 Authorization 头 → 下载解码 PixelMap)
        │   ├── InkCanvas.ets    归一化笔迹画板(契约同 score.Stroke:pts 0~1)
        │   ├── ScorePad.ets     触屏评分盘(≤10 单键即打;>10 十位/个位两键合成;±0.5;小数直录)
        │   ├── SliceStage.ets   切片舞台:图 + 笔迹叠加 + 灰度/反色 + 点按全屏细看
        │   ├── Charts.ets       Canvas 折线/柱状/横条(对齐 common.js chartLine 口径)
        │   ├── StaffNav.ets     教职员 ☰ 导航(全工作台页共用)
        │   └── Ui.ets           TitleBar/Panel/Error/Empty/Pill/KV/按钮 等轻量件
        └── pages/               22 个路由页(Index 分发/Login/ForcePwd/Me/ImageViewer +
                                 student 4 页 + teacher 11 页 + admin 1 页)
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
| 通用 | Index / Login / ForcePwd / Me | 服务器地址检测+多址记忆、native 登录、首登强制改密、改密、多服务器切换、登出 |
| 学生/家长 | StudentHome(页签:成绩/趋势/我的) | 已发布考试列表(缺考/复核角标)、家长多孩切换 |
| | StudentExamDetail | 总分卡(年级/班级排名/均分)、逐题对错(错题本过滤)、知识点掌握度、主观切片、整卷回溯多页、复核申请提交与回复、错题重练卡入口 |
| | StudentTrends | 按学科得分率曲线+班级对照线、逐场名次进退 |
| | Practice | 错题重练卡摘要 + 可打印 SVG 另存 |
| 教师 | TaskList(首页) | 我的任务(按题/按班/建卷人兜底、进度、截止、深链阅卷) |
| | ExamList | 阶段过滤/搜索、状态机主操作(去扫描/处理异常/阅卷/发布/成绩册) |
| | ExamHub | 阶段卡、逐题进度深链、异常清单裁决(推荐考号/缺考/作废/AB 指派)、重复卷保留、缺扫名单、作废卷恢复、⚑队列、复核回复+拉黑、元数据编辑、答案修正重统、发布/撤回、导出下载、删除 |
| | Grader | 切片预加载(后 8 张)、触屏评分盘、✓✗⚑、画笔批注(持久化)、小问逐问、选做涂选展示、匿名隐藏考号、409 拉取最新、自动跳转、待复核队列 |
| | UploadScans | 相册多选/拍照 → 沙箱拷贝 → 5 张分批接力、失败续传、跨批合并/告警反馈 |
| | ManualScores | 手工成绩逐格直录、缺考登记 |
| | CreateExam | 手工成绩免版式建考 / 扫描考试建档(版式 JSON 文件导入+班级名册+AB 卷开关) |
| | Analytics | 统计摘要、分数段、逐题难度/区分度、知识点掌握度、班级对比、五等/21 级赋分 |
| | StaffTrends | 班级均分走势、逐生跨考对齐与名次进退 |
| | Aggregate | 年级总分工表(横向滚动,缺考/无卷标记,可见科目口径提示) |
| | Assign | 按题/按班阅卷人分配 + 截止时间 |
| | BankList | 题库只读列表(题干缩略图) |
| 管理 | UserList | 账号列表/搜索、新建(全角色字段)、重置密码、删除 |

**留在桌面 Web**(App 内提供文案提示):设计器出卡、试卷版面识别、题库录入/编辑、组织架构与场次表维护、CSV 批量导入、考场编排微调。

## 四、关键实现决策

1. **图片鉴权**:ArkUI `Image` 不能附带请求头 → 统一走 `OgImage`/`ImageCache`:
   `OgNet.getBytes`(带 Bearer)→ `image.createImageSource` 解码(可选降采样)→ 内存 LRU(80 张,FIFO 淘汰);Grader 预加载直接 `ImageCache.prefetch`。
2. **笔迹契约**:`Stroke{color, pts[]}`,pts 为相对切片显示区归一化坐标(0~1),与 web 端完全同源;批注/评分同一次 `POST /grades` 持久化。
3. **评分乐观锁**:所有 `POST /grades` 携带读到的 `ver`;409 时 toast 并重拉本题保持卷位(与 web 同语义)。
4. **小问(版式 v3)**:打分写 `part` 维度(逐问独立 ver);mark/ink 变更按 web 语义同步写每个小问。
5. **上传**:picker 返回的 `file://media` URI 先 `fs.copyFile` 进沙箱缓存(multipart `filePath` 要求真实路径),5 张/批接力,失败即停可续传(与 web 的 15 张/批语义一致、批更小)。
6. **PDF 拆页**:一期仅收 JPEG/PNG,PDF 引导电脑端(pdf.js 路径);`@ohos.file.pdf` 拆页留待真机验证后启用(见「已知边界」)。
7. **下载交付**:`OgNet.downloadToPicker` = 下载到内存(DocumentViewPicker.save)→ 写入用户选择的位置;成绩册/数据包/成绩单/练习卡均走此路径。
8. **强制改密**:登录响应 `mustChangePassword` → ForcePwdPage;服务端闸门(403)兜底。
9. **多服务器**:token 按服务器隔离,切换即登出;服务器列表存 preferences(最多 6 条)。

## 五、构建与真机验证(需 DevEco Studio 5.0+)

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

- `@ohos.file.pdf` 拆页、阅卷放大镜(带笔迹的捏合缩放)、知识点雷达图、离线缓存、考场编排:留待真机验证/后续批次;
- 题库、组织架构、场次表、CSV 导入:电脑端操作;
- 推送通知:需要华为推送服务与云端依赖,与"数据 100% 本地化"定位冲突,暂不做;
- 上架:企业内部分发(hdc 安装/企业签名)或 AppGallery(需软著与教育类资质)。
