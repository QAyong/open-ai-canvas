// 核验记录的前端演示存储：只在当前标签页内存里，刷新即丢。
// 接入后端后替换为 services/api 的 /api/compliance 调用与 React Query，步骤进度改由 Agent 工具事件推进。

import { create } from "zustand";

import { buildMockFaceResult, complianceSteps, type ComplianceCheck, type NewComplianceCheck } from "./compliance-check";

export const MOCK_STEP_MS = 900;

type ComplianceCheckStore = {
    checks: Record<string, ComplianceCheck>;
    create: (input: NewComplianceCheck, stepMs?: number) => string;
};

export const useComplianceCheckStore = create<ComplianceCheckStore>((set) => ({
    checks: {},
    create: (input, stepMs = MOCK_STEP_MS) => {
        const id = crypto.randomUUID();
        set((state) => ({ checks: { ...state.checks, [id]: { ...input, id, status: "running", stepsDone: 0, createdAt: Date.now(), result: buildMockFaceResult(input) } } }));
        complianceSteps.forEach((_, index) => {
            setTimeout(
                () => {
                    set((state) => {
                        const current = state.checks[id];
                        if (!current) return state;
                        const stepsDone = index + 1;
                        return { checks: { ...state.checks, [id]: { ...current, stepsDone, status: stepsDone >= complianceSteps.length ? "done" : "running" } } };
                    });
                },
                stepMs * (index + 1),
            );
        });
        return id;
    },
}));
