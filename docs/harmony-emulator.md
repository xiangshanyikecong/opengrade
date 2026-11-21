# 鸿蒙客户端在模拟器/真机上的运行与验收

> 配套文档:[harmony-app.md](harmony-app.md)(架构与功能矩阵)。本文只讲“怎么跑起来、怎么验”。

## 零、先决条件

| 项 | 要求 |
|---|---|
| DevEco Studio | 5.0+ (HarmonyOS NEXT / API 12) |
| 鸿蒙 SDK | API 12 (5.0.0(12)),含 System Images(模拟器用) |
| 华为开发者账号 | 仅自动签名需要(真机/模拟器装包都要签名) |
| 服务端 | 本机或校内一台机器跑 `ogserver`,手机/模拟器与其同网 |

## 一、装模拟器

1. DevEco Studio → **Tools → Device Manager**(设备管理器);
2. 点 **+ → Local Emulator / 使用本地模拟器**,选设备(如 Mate 60 / API 12),系统镜像会提示下载(在 SDK Manager → HarmonyOS SDK → System Images 里勾对应版本);
3. **Windows 需要开启虚拟化**:系统设置 → 应用 → 可选功能 → 虚拟机平台(Win11 专业版可直接开 Hyper-V;Win10 需“启用或关闭 Windows 功能 → 虚拟机平台”)。DevEco 首次会提示开启,按提示走;不成功会在模拟器启动时报“HAXM/Hyper-V 未启用”;
4. 回到 Device Manager 点 ▶ 启动模拟器,等它进桌面。

> 没有模拟器也能验收:真机开启“开发者选项 → USB 调试”,用数据线连电脑,DevEco 里设备列表会直接出现该机。

## 二、签名(没签名装不上)

DevEco Studio → **File → Project Structure → Signing Configs**:

1. 勾选 **Automatically generate signature**(需先登录华为账号);
2. 勾 **Support HarmonyOS**;
3. Bundle Name 保持 `org.opengrade.app`;
4. Apply → OK。

生成后 `harmony/build-profile.json5` 里的 `signingConfigs` 会由 IDE 自动写入(**这个改动在你自己机器上,不要提交回仓库**,仓库里保持空数组即可)。

## 三、跑起来

**方式 A(推荐,IDE 一键)**:
选中模拟器 → 工具栏 **Run 'entry'** → 自动编译 → 安装 → 启动。

**方式 B(命令行)**:

```powershell
# 在 harmony/ 目录下执行(用 IDE 自带 node 与 hvigor)
"D:\DevEco Studio\tools\node\node.exe" `
  "D:\DevEco Studio\tools\hvigor\bin\hvigorw.js" `
  --mode module -p module=entry@default -p product=default assembleHap
```

产物:`harmony/entry/build/default/outputs/default/entry-default-signed.hap`(签名后)。未签名产物不能直接安装。

## 四、连服务器(最关键的一步)

服务端在**宿主机**上跑:

```bash
go build -o ogserver ./cmd/ogserver
./ogserver -addr :8787 -seed-demo
```

手机/模拟器里填的地址**不能是 `127.0.0.1`**——在模拟器里它指的是模拟器自己。按以下顺序试:

1. **宿主机局域网 IP**(推荐):Windows 执行 `ipconfig`,取 IPv4(如 `192.168.1.10`),App 里填 `http://192.168.1.10:8787`;
2. 若不通,试 `http://10.0.2.2:8787`(Android 模拟器惯例的宿主机别名,部分鸿蒙模拟器兼容);
3. 再不行,检查 **Windows 防火墙**:首次启动 ogserver 会弹“允许 Java/ogserver 通过防火墙”,务必勾 **专用网络**;没弹过就去「Windows Defender 防火墙 → 允许应用通过防火墙」放行 8787。

**快速自检**:在模拟器自带浏览器里打开 `http://<宿主机IP>:8787/healthz`,能看到 `{"status":"ok","service":"opengrade","version":"0.3.0"}` 就说明通了;App 里点「检测」按钮也是同一套逻辑。

**若提示明文被拒**(App 内报错类似 “网络异常:无法连接服务器”,而浏览器又能开):说明系统拦了 HTTP 明文,在 `harmony/entry/src/main/module.json5` 的 `module` 下加一行元数据后重新编译:

```json5
"metadata": [
  { "name": "ohos.net.http.CleartextTraffic", "value": "true" }
]
```

## 五、验收清单(按角色)

测试账号由 `-seed-demo` 生成:`admin/admin123`、`leader1/leader123`、`teacher1/teach123`(数学)、`s0001/student123`(学生,考号 00000001)。

### 学生端(s0001)
- [ ] 登录页填服务器地址 → 点「检测」显示已连接 → 登录成功落到成绩列表;
- [ ] 列表有考试卡片(学科/日期/得分/年级排名/班级均分);缺考显示“缺考”徽标;
- [ ] 进详情:总分卡、逐题对错(错题标红)、点「只看错题」过滤生效;
- [ ] 知识点掌握度横条有数据且薄弱排前;
- [ ] 主观题切片图能显示(鉴权图片走 Bearer 下载);整卷回溯可点开大图、左右翻页;
- [ ] 趋势页有按学科曲线;提交一条复核申请 → 列表出现“复核处理中”角标。

### 家长端
- [ ] 顶栏可切换子女,切换后列表随之变化(需先在电脑端建绑定两个孩子的 parent 账号)。

### 教师端(teacher1)
- [ ] 「我的任务」默认首页,任务带进度与截止;点任务直达该题阅卷;
- [ ] 建「手工成绩考试」→ 详情页录成绩保存 → 电脑 web 端能看到同数据;
- [ ] 建常规考试(版式 JSON 用电脑端设计器导出,经「文件」导入)→ 手机「拍照」上传答题卡 → 详情页已扫数上涨、异常清单出现;
- [ ] 异常卷点「推荐考号…」出候选 → 一键指派成功;
- [ ] **触屏阅卷**:打分(≤10 单键即打 / >10 两键合成)、±0.5、✓✗⚑、画笔批注 → **电脑 web 端刷新,分数与笔迹完全一致**;
- [ ] 主观题拆小问时,逐问打分后总分自动汇总、进度条同步;
- [ ] **双开互改**:电脑 grader 与手机同时打开同一份卷,电脑先改分,手机再打分 → 手机弹出“刚被他人改过”并拉取最新,不覆盖;
- [ ] 发布成绩 → 学生端立刻可查;撤回 → 立刻不可查;
- [ ] 学情分析(统计卡/分数段柱状/难度区分度/知识点/班级对比)、年级总表、导出 xlsx 保存到「文件」。

### 管理端(admin,首登会强制进改密页 —— 这本身就是闸门验收点)
- [ ] 新建用户/重置密码生效;被重置账号在电脑端的会话立即掉线。

## 六、常见问题

| 现象 | 原因与处理 |
|---|---|
| 模拟器启动失败(HAXM/Hyper-V) | 开「虚拟机平台」,BIOS 里开 VT-x/AMD-V,重启后重试 |
| 装不上包(签名错误) | Signing Configs 未配或证书过期;重新自动签名 |
| App 里连不上服务器 | 填了 127.0.0.1(改成宿主机 LAN IP);防火墙未放行 8787;手机与电脑不在同一网段 |
| 登录一直转圈/报网络异常 | 服务端没起或端口被占;先在浏览器开 /healthz 自检 |
| 切片图不显示 | 多为鉴权图片加载失败:确认 token 未过期(退出重登) |
| 拍照不可用 | 模拟器无摄像头属正常,改用「相册多选」验收扫描上传链路 |
| 应用闪退 | DevEco → HiLog 查看崩溃栈,或 `hdc shell hilog` |

## 七、命令行辅助(可选)

```bash
# 看设备是否连上
hdc list targets

# 安装已签名 HAP
hdc install entry-default-signed.hap

# 启动应用(bundleName + abilityName)
hdc shell aa start -a EntryAbility -b org.opengrade.app

# 实时日志
hdc shell hilog | grep opengrade
```
