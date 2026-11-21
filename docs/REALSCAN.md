# 真实扫描回归集(REALSCAN)

synth 模拟的损伤(透视/旋转/光照不均/噪声/JPEG)很强,但终归是合成分布。
真实世界的异常扫描件——订书钉遮挡、边缘裁切、复印件、手机斜拍、污染褶皱——
只有真实样本能守住判读算法的回归。本目录就是这些样本的家(脚手架来自
open-mcr 的 e2e fixture 思路,判读走 OpenGrade 同一条 `omr.Read` 管线)。

## 添加一个样本

1. 把扫描件(PNG/JPEG,EXIF 方向会自动转正)放进 `omr/testdata/realscan/`,
   文件名建议带损伤类型前缀,如 `stapled_01.jpg`、`photocopy_faded_03.jpg`。
2. 把对应版式 `.layout.json` 也放进同目录(多场考试的样本可共用一份版式)。
3. 在 `manifest.json` 的 `samples` 数组追加一条:

```json
{
  "image": "stapled_01.jpg",
  "layout": "default.layout.json",
  "reg": "20250101",
  "answers": ["A", "B", "AC", "T"],
  "allowWarnings": ["涂写偏浅"],
  "note": "订书钉遮挡右上角,考号仍可读"
}
```

字段说明:

| 字段 | 说明 |
|---|---|
| `image` / `layout` | 相对本目录的文件名 |
| `reg` | 期望考号;留空跳过考号校验 |
| `answers` | 期望逐题作答(可只写关心的前 N 题);留空跳过作答校验 |
| `allowWarnings` | 允许出现的告警子串(如已知的偏浅提示);**未列出的告警一律算失败** |
| `note` | 人看的说明:什么损伤、当初为什么留着它 |

4. `go test ./omr/ -run TestRealScanRegression -v` 通过后提交。
   样本里**不要包含可识别的学生个人信息**(拍之前遮掉姓名手写区即可)。

## 为什么期望要这么严

回归集的价值在于"任何判读改动都必须解释每一处行为变化"。新增告警、
丢一个考号、多判一道题——都会在这里炸出来,逼着改动者回答:
是改进(人工核对后更新 manifest)还是回归(修代码)。
