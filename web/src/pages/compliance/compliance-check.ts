// 合规页自己的前端常量：能力目录、步骤文案、发给 Agent 的首条消息。
// 判定规则（相似度阈值、整体结论）只在后端，页面从核验结果里读取。

import { Fingerprint, ScanFace, ShieldAlert, type LucideIcon } from "lucide-react";

export type ComplianceSkillId = "face-compare" | "ip-brand" | "sensitive-words";

export const complianceSkills: Array<{ id: ComplianceSkillId; title: string; hint: string; icon: LucideIcon; ready: boolean }> = [
    { id: "face-compare", title: "人脸比对", hint: "核验素材中的授权肖像", icon: ScanFace, ready: true },
    { id: "ip-brand", title: "IP / 品牌检查", hint: "即将上线", icon: Fingerprint, ready: false },
    { id: "sensitive-words", title: "敏感词检查", hint: "即将上线", icon: ShieldAlert, ready: false },
];

/** 输入框的提交方式：选了人脸比对或放了图片就是"创建核验"，素材必须齐全；否则是纯文字，直接发给 Agent 对话。 */
export function resolveComposerSubmit(input: { skillId: ComplianceSkillId | null; hasTarget: boolean; baseCount: number; prompt: string }): { mode: "check" | "chat"; canSubmit: boolean; missing: string[] } {
    const check = input.skillId === "face-compare" || input.hasTarget || input.baseCount > 0;
    if (!check) return { mode: "chat", canSubmit: input.prompt.trim().length > 0, missing: [] };
    const missing = [!input.hasTarget && "待核验素材", input.baseCount === 0 && "授权底图"].filter((item): item is string => Boolean(item));
    return { mode: "check", canSubmit: missing.length === 0, missing };
}

/** 结果页步骤条文案，顺序与后端 compliance.StepCount 的三个阶段一致。 */
export const complianceSteps = ["检测人脸", "比对底图", "生成结论"] as const;

/** 提交核验时发给 Agent 的首条消息：用户的描述加上素材清单。Agent 通过 compliance_get_check 读结果，看不到图片本身。 */
export function buildAgentPrompt(input: { prompt: string; subjectName: string; targetName: string; baseCount: number }) {
    const detail = [`待核验素材：${input.targetName}`, `授权底图 ${input.baseCount} 张`, input.subjectName && `授权人：${input.subjectName}`].filter(Boolean).join("；");
    return `${input.prompt || "请先读取核验结果，再解读并给出处置建议。"}（${detail}）`;
}
