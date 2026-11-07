import { http } from "@/services/api/request";

export type ComplianceKind = "face_compare";
export type ComplianceStatus = "running" | "done" | "failed";
export type FaceVerdict = "match" | "suspect" | "mismatch";
export type OverallVerdict = "pass" | "review" | "risk" | "no_face";
/** 画风：写实照片时引擎相似度是主要依据；风格化或无法确认时只供参考，结论不会是通过。 */
export type ImageStyle = "realistic" | "stylized" | "unclear";

/** 已上传图片资源的引用；用 resourceFileUrl 取展示地址。 */
export type ComplianceImageRef = { resourceId: string; name: string };

/** 人脸框用占图片宽高的百分比表示，和图片实际像素无关。 */
export type FaceBox = { x: number; y: number; w: number; h: number };

export type ReportFace = {
    /** 从 1 开始，对应结果页里的人脸编号。 */
    index: number;
    box: FaceBox;
    /** 与最接近的对比图片的相似度，0-100，来自比对引擎。 */
    similarity: number;
    bestBaseIndex: number;
    verdict: FaceVerdict;
    /** Agent 写下的判断依据与限制。 */
    basis: string[];
    limitations: string[];
};

/** 核验报告：数字、人脸框和结论由服务端按引擎证据与阈值生成，依据、小结和建议是 Agent 的判断。 */
export type ComplianceReport = {
    overall: OverallVerdict;
    style: ImageStyle;
    summary: string;
    faces: ReportFace[];
    suggestions: string[];
    /** 只在 Agent 把结论往上提时出现。 */
    escalation?: { to: OverallVerdict; reason: string };
    humanReview: { required: boolean; reason?: string };
    disclaimer: string;
};

export type ComplianceStepState = "done" | "active" | "pending";

/** 结果页步骤条的一步，由比对工具实际产出的证据推导，反映 Agent 做到了哪一步。 */
export type ComplianceStep = { id: string; label: string; state: ComplianceStepState };

export type ComplianceCheck = {
    id: string;
    kind: ComplianceKind;
    prompt: string;
    subjectName: string;
    status: ComplianceStatus;
    steps: ComplianceStep[];
    /** 承载这次核验的 Agent 运行，以及当时使用的合规技能版本。 */
    runId: string;
    skillVersion: string;
    /** 相似度达到 match 为一致，介于 suspect 与 match 之间为疑似；由后端给出，前端不写死。 */
    thresholds: { match: number; suspect: number };
    target: ComplianceImageRef;
    bases: ComplianceImageRef[];
    /** 只在 status 为 done 时存在。 */
    result?: ComplianceReport;
    error?: string;
    createdAt: string;
    updatedAt: string;
};

/** 承载核验的 Agent 使用的系统渠道文本模型，需要支持图片输入。 */
export type ComplianceAgentModel = { channelId: string; channelModelKey: string };

export type CreateComplianceCheckInput = {
    kind: ComplianceKind;
    prompt: string;
    subjectName: string;
    target: ComplianceImageRef;
    bases: ComplianceImageRef[];
    agentModel: ComplianceAgentModel;
};

export function createComplianceCheck(input: CreateComplianceCheckInput) {
    return http.post<ComplianceCheck>("/compliance/checks", input);
}

export function getComplianceCheck(id: string, signal?: AbortSignal) {
    return http.get<ComplianceCheck>(`/compliance/checks/${encodeURIComponent(id)}`, { signal });
}
