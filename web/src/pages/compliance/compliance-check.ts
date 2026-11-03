// 合规核验的类型、能力目录与演示用比对逻辑。
// 目前没有后端：结果由 buildMockFaceResult 按文件名生成，页面上会标注"演示数据"。
// 接入后端后，结果改由 /api/compliance 返回，这里只保留类型、目录和阈值。

import { Fingerprint, ScanFace, ShieldAlert, type LucideIcon } from "lucide-react";

export type ComplianceSkillId = "face-compare" | "ip-brand" | "sensitive-words";

export const complianceSkills: Array<{ id: ComplianceSkillId; title: string; hint: string; icon: LucideIcon; ready: boolean }> = [
    { id: "face-compare", title: "人脸比对", hint: "核验素材中的授权肖像", icon: ScanFace, ready: true },
    { id: "ip-brand", title: "IP / 品牌检查", hint: "即将上线", icon: Fingerprint, ready: false },
    { id: "sensitive-words", title: "敏感词检查", hint: "即将上线", icon: ShieldAlert, ready: false },
];

/** 相似度（百分制）达到该值视为一致，介于两个阈值之间为疑似。 */
export const FACE_MATCH_THRESHOLD = 85;
export const FACE_SUSPECT_THRESHOLD = 70;

export type FaceVerdict = "match" | "suspect" | "mismatch";
export type OverallVerdict = "pass" | "review" | "risk" | "no_face";

export type ComplianceImage = { url: string; name: string };

/** 人脸框用占图片宽高的百分比表示，和图片实际像素无关。 */
export type FaceBox = { x: number; y: number; w: number; h: number };

export type FaceItem = {
    index: number;
    box: FaceBox;
    /** 与最接近的授权底图的相似度，0-100。 */
    similarity: number;
    bestBaseIndex: number;
    verdict: FaceVerdict;
};

export type FaceCompareResult = {
    overall: OverallVerdict;
    faces: FaceItem[];
};

/** 结果页的步骤条：stepsDone 为已完成的步数，结果随步骤逐块出现。 */
export const complianceSteps = ["检测人脸", "比对底图", "生成结论"] as const;

export type ComplianceCheck = {
    id: string;
    skillId: ComplianceSkillId;
    prompt: string;
    subjectName: string;
    target: ComplianceImage;
    bases: ComplianceImage[];
    status: "running" | "done";
    stepsDone: number;
    createdAt: number;
    /** 演示数据在创建时就算好，由 stepsDone 决定显示多少；接入后端后随工具事件逐步到达。 */
    result?: FaceCompareResult;
};

export type NewComplianceCheck = Pick<ComplianceCheck, "skillId" | "prompt" | "subjectName" | "target" | "bases">;

/** 提交核验时发给 Agent 的首条消息：用户的描述加上素材清单，Agent 看不到图片本身。 */
export function buildAgentPrompt(input: Pick<NewComplianceCheck, "prompt" | "subjectName" | "target" | "bases">) {
    const detail = [`待核验素材：${input.target.name}`, `授权底图 ${input.bases.length} 张`, input.subjectName && `授权人：${input.subjectName}`].filter(Boolean).join("；");
    return `${input.prompt || "请帮我核验这次人脸比对。"}（${detail}）`;
}

export function faceVerdictOf(similarity: number): FaceVerdict {
    if (similarity >= FACE_MATCH_THRESHOLD) return "match";
    if (similarity >= FACE_SUSPECT_THRESHOLD) return "suspect";
    return "mismatch";
}

/** 有人脸与授权人一致且没有其他人脸才算通过；多出来的人脸或"疑似"都转人工确认。 */
export function overallVerdictOf(faces: FaceItem[]): OverallVerdict {
    if (!faces.length) return "no_face";
    const matched = faces.filter((face) => face.verdict === "match").length;
    const suspect = faces.some((face) => face.verdict === "suspect");
    if (matched && matched === faces.length) return "pass";
    if (matched || suspect) return "review";
    return "risk";
}

function seedOf(text: string) {
    let hash = 2166136261;
    for (const char of text) {
        hash ^= char.charCodeAt(0);
        hash = Math.imul(hash, 16777619);
    }
    return hash >>> 0;
}

const mockFaceBoxes: FaceBox[] = [
    { x: 14, y: 14, w: 30, h: 42 },
    { x: 56, y: 22, w: 28, h: 38 },
];

export function buildMockFaceResult(input: Pick<NewComplianceCheck, "target" | "bases">): FaceCompareResult {
    const seed = seedOf(`${input.target.name}|${input.bases.map((base) => base.name).join(",")}`);
    const count = seed % 3 === 0 ? 2 : 1;
    const faces: FaceItem[] = mockFaceBoxes.slice(0, count).map((box, index) => {
        const similarity = index === 0 ? 60 + (seed % 38) : 38 + ((seed >> 3) % 30);
        return { index: index + 1, box, similarity, bestBaseIndex: (seed + index) % input.bases.length, verdict: faceVerdictOf(similarity) };
    });
    return { overall: overallVerdictOf(faces), faces };
}
