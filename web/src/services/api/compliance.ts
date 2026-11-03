import { http } from "@/services/api/request";

export type ComplianceKind = "face_compare";
export type ComplianceStatus = "running" | "done" | "failed";
export type FaceVerdict = "match" | "suspect" | "mismatch";
export type OverallVerdict = "pass" | "review" | "risk" | "no_face";

/** 已上传图片资源的引用；用 resourceFileUrl 取展示地址。 */
export type ComplianceImageRef = { resourceId: string; name: string };

/** 人脸框用占图片宽高的百分比表示，和图片实际像素无关。 */
export type FaceBox = { x: number; y: number; w: number; h: number };

export type FaceItem = {
    /** 从 1 开始，对应结果页里的人脸编号。 */
    index: number;
    box: FaceBox;
    /** 与最接近的授权底图的相似度，0-100。 */
    similarity: number;
    bestBaseIndex: number;
    verdict: FaceVerdict;
};

export type FaceCompareResult = { overall: OverallVerdict; faces: FaceItem[] };

export type ComplianceCheck = {
    id: string;
    kind: ComplianceKind;
    prompt: string;
    subjectName: string;
    status: ComplianceStatus;
    stepsDone: number;
    stepCount: number;
    /** 相似度达到 match 为一致，介于 suspect 与 match 之间为疑似；由后端给出，前端不写死。 */
    thresholds: { match: number; suspect: number };
    target: ComplianceImageRef;
    bases: ComplianceImageRef[];
    /** 只在 status 为 done 时存在。 */
    result?: FaceCompareResult;
    error?: string;
    createdAt: string;
    updatedAt: string;
};

export type CreateComplianceCheckInput = {
    kind: ComplianceKind;
    prompt: string;
    subjectName: string;
    target: ComplianceImageRef;
    bases: ComplianceImageRef[];
};

export function createComplianceCheck(input: CreateComplianceCheckInput) {
    return http.post<ComplianceCheck>("/compliance/checks", input);
}

export function getComplianceCheck(id: string, signal?: AbortSignal) {
    return http.get<ComplianceCheck>(`/compliance/checks/${encodeURIComponent(id)}`, { signal });
}
