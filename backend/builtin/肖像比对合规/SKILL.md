---
name: 肖像比对合规
description: 合规页「人脸比对」核验的工作法：先看图判断画风，再检测人脸、逐脸比对授权底图，最后用 compliance_submit_report 提交审慎措辞的报告。只在本轮工具表里有 compliance_* 工具时使用。
metadata:
  version: "1.0.0"
  author: 影策
  owner: yingce-system
  skillId: "yingce-portrait-face-compare"
  tag: others
  sortWeight: 900
  source: 3
---

# 肖像比对合规

## 适用与边界

只在合规核验的运行里使用：本轮工具表里有 `compliance_get_check`、`compliance_inspect_images`、`compliance_detect_faces`、`compliance_compare_faces`、`compliance_submit_report`。画布对话里没有这些工具，不要套用本技能。

任务：判断「待核验图」里的人是否就是「授权底图」里的授权人。结果是合规审查的辅助材料，不是司法鉴定、律师意见或法院判断。

不做：根据图片识别私人身份、查找任何人的个人信息、给出确定性的法律结论。

## 流程（按顺序，不要跳步）

1. `compliance_get_check`：读本次核验的授权人、图片名、阈值和用户说明。
2. `compliance_inspect_images`：一次看全部图片（省略 `images`），purpose 写明要核实的三件事——画风是写实照片还是插画 / 3D / 风格化、每张图的人脸数量与朝向遮挡、清晰度。
3. `compliance_detect_faces`：对 `target` 和每一张 `base-N` 各调用一次。
4. `compliance_compare_faces`：待核验图的**每一张脸**都至少与每张授权底图的人脸比对一次。底图有多张脸时，选质量最高、最像正脸的那张作为 `baseFace`。待核验图没有人脸时不用比对。
5. `compliance_submit_report`：按下面的规范提交，一次成功后不要再调用任何工具。
6. 用一两句话告诉用户总体结果和最需要注意的一点。不要重复贴相似度表，页面上已经有了。

不要调用 `ask_user`：核验运行里没有人实时回答，提问会让运行提前结束。信息不够时按已有的材料如实判断，把缺失写进 `limitations`。

## 怎么判断

- **相似度和结论只来自引擎**：每张脸的相似度、一致 / 疑似 / 不一致、整体结论，都由服务端按引擎证据与固定阈值生成。你不能改判，也不要在文字里写与工具返回不一致的数字。
- **画风决定可信度**：
  - `realistic`（写实照片）：引擎相似度可作为主要依据。
  - `stylized`（插画、3D、AI 角色等风格化）或 `unclear`：人脸向量对风格化图不可靠，相似度只供参考，服务端不会给出「通过」。依据里要写风格化削弱了哪些可比特征。
- **你的价值在判断**：说明哪些特征相近或不同、哪些因素限制了判断、下一步该怎么办。

比对维度和风险信号见 `references/risk-rubric.md`，措辞与人工复核触发条件见 `references/wording.md`，提交字段与示例见 `references/report-guide.md`。需要细节时再读，不必每次都读。

## 提交规范

`compliance_submit_report` 里你只写判断与文字：

- `style`：realistic / stylized / unclear，依据第 2 步看图。
- `summary`：一两段话，先说结论再说原因，≤600 字。
- `faces`：待核验图**每张检出的脸**一条（`index` 与检测结果一致）。`basis` 写可观察的特征维度（脸型与下颌线、五官布局、眼眉、鼻口、标志性特征），`limitations` 写角度、遮挡、清晰度、风格化等限制。
- `suggestions`：具体可执行的处置，如补充授权底图、换用清晰正脸素材、转人工复核；不要写泛泛的套话。
- `escalateTo`：只在你有明确理由时把整体结论升为 `review` 或 `risk`，并写 `escalateReason`。不能压低。

提交被拒时，错误信息会说明缺什么（没比对的脸、没写判断的脸、措辞不合规），补齐后重试。

## 措辞

用「存在疑似风险」「建议人工复核」「当前比对范围内」；不用「确认侵权」「确认同一人」「未侵权」「绝对安全」「可以放心商用」。引用相似度时写成「引擎相似度 X」，不要写成「X% 是同一个人」。

## 出错时

- 工具报错先按提示补做缺失步骤。
- 比对引擎暂时不可用：重试一次；仍失败就停止，用一句话如实告诉用户引擎不可用，不要编造结果，也不要提交没有证据的报告。
