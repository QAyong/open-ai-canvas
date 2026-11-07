// 合规页自己的前端常量：能力目录、输入框提交方式、承载核验的 Agent 模型。
// 判定规则（相似度阈值、整体结论）只在后端，页面从核验结果里读取；步骤和任务描述也由后端给出。

import { Fingerprint, ScanFace, type LucideIcon } from "lucide-react";

import { selectImageInspectionModel } from "@/lib/canvas/image-to-previs-agent";
import type { ComplianceAgentModel } from "@/services/api/compliance";
import { modelOptionName, resolveModelRequestConfig, type AiConfig } from "@/stores/use-config-store";

export type ComplianceSkillId = "face-compare" | "web-batch-compare";

export const complianceSkills: Array<{ id: ComplianceSkillId; title: string; hint: string; icon: LucideIcon; ready: boolean }> = [
    { id: "face-compare", title: "两张图片对比", hint: "评估两张图片的肖像相似风险", icon: ScanFace, ready: true },
    { id: "web-batch-compare", title: "网络批量对比", hint: "网络检索候选并批量对比（即将上线）", icon: Fingerprint, ready: false },
];

/** 输入框的提交方式：选了两张图片对比或放了图片就是"创建核验"，素材必须齐全；否则是纯文字，直接发给 Agent 对话。 */
export function resolveComposerSubmit(input: { skillId: ComplianceSkillId | null; hasTarget: boolean; baseCount: number; prompt: string }): { mode: "check" | "chat"; canSubmit: boolean; missing: string[] } {
    const check = input.skillId === "face-compare" || input.hasTarget || input.baseCount > 0;
    if (!check) return { mode: "chat", canSubmit: input.prompt.trim().length > 0, missing: [] };
    const missing = [!input.hasTarget && "图片 A", input.baseCount === 0 && "图片 B"].filter((item): item is string => Boolean(item));
    return { mode: "check", canSubmit: missing.length === 0, missing };
}

/**
 * 核验由 Agent 看图完成，需要后端受管、支持图片输入的文本模型：优先用用户当前选的模型，不行就挑第一个可用的。
 * 没有可用模型时抛错，由页面提示去模型能力设置里开启图片输入。
 */
export function pickComplianceAgentModel(config: AiConfig): ComplianceAgentModel {
    const model = selectImageInspectionModel(config, config.model);
    if (!model) throw new Error("当前没有配置支持图片输入的文本模型，请在模型能力设置中为一个文本模型开启图片输入后重试");
    const { channelId } = resolveModelRequestConfig({ ...config, model }, model);
    const channelModelKey = modelOptionName(model);
    if (!channelId || !channelModelKey) throw new Error("核验需要使用后端受管的文本模型，请换一个系统渠道里的模型");
    return { channelId, channelModelKey };
}
