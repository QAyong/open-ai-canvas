import { describe, expect, test } from "bun:test";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter, Route, Routes } from "react-router";

import { navigationTools } from "../src/constant/navigation-tools";
import CompliancePage from "../src/pages/compliance";
import { ComplianceFaceResult } from "../src/pages/compliance/compliance-face-result";
import { pickComplianceAgentModel, resolveComposerSubmit } from "../src/pages/compliance/compliance-check";
import type { ComplianceCheck, ComplianceStep } from "../src/services/api/compliance";
import type { AiConfig } from "../src/stores/use-config-store";
import { useGlobalAgentStore } from "../src/stores/use-global-agent-store";

const source = (path: string) => readFileSync(resolve(import.meta.dir, path), "utf8");

function renderPage(path: string) {
    return renderToStaticMarkup(
        <QueryClientProvider client={new QueryClient()}>
            <MemoryRouter initialEntries={[path]}>
                <Routes>
                    <Route path="/compliance" element={<CompliancePage />} />
                    <Route path="/compliance/:id" element={<CompliancePage />} />
                </Routes>
            </MemoryRouter>
        </QueryClientProvider>,
    );
}

const stepsOf = (states: [ComplianceStep["state"], ComplianceStep["state"], ComplianceStep["state"], ComplianceStep["state"]]): ComplianceStep[] =>
    [
        ["inspect", "查看图片"],
        ["detect", "检测人脸"],
        ["compare", "比对图片"],
        ["report", "生成结论"],
    ].map(([id, label], index) => ({ id, label, state: states[index] }));

const baseCheck: ComplianceCheck = {
    id: "chk_1",
    kind: "face_compare",
    prompt: "",
    subjectName: "张三",
    status: "done",
    steps: stepsOf(["done", "done", "done", "done"]),
    runId: "agrun1",
    skillVersion: "v1",
    thresholds: { match: 85, suspect: 70 },
    target: { resourceId: "res_t", name: "still.png" },
    bases: [{ resourceId: "res_b", name: "base.png" }],
    result: {
        overall: "review",
        style: "realistic",
        summary: "图片 A 检出 1 张人脸，与图片 B 的相似度处于疑似区间。",
        faces: [{ index: 1, box: { x: 10, y: 10, w: 30, h: 40 }, similarity: 78, bestBaseIndex: 0, verdict: "suspect", basis: ["脸型与下颌线接近"], limitations: ["侧脸角度限制了判断"] }],
        suggestions: ["换用一张清晰正脸图片后重新对比"],
        humanReview: { required: true, reason: "存在疑似的人脸" },
        disclaimer: "本结果为面部特征相似度的技术分析，仅作合规审查的辅助材料。",
    },
    createdAt: "2026-11-03T10:00:00Z",
    updatedAt: "2026-11-03T10:00:03Z",
};

const visionConfig = {
    channels: [
        {
            id: "plain",
            scope: "system",
            models: ["plain-text"],
            modelCosts: [
                {
                    model: "plain-text",
                    capability: "text",
                    protocol: "chat-completion",
                    billingMode: "fixed_request",
                    unitPriceMicrocredits: 1,
                    capabilityConfig: { version: 1, text: { contextWindowTokens: 128000, maxOutputTokens: 16384, references: { promptMaxChars: 32000, maxImages: 0, maxImageBytes: 0, maxVideos: 0, maxVideoBytes: 0 } } },
                },
            ],
        },
        {
            id: "vision",
            scope: "system",
            models: ["vision-text"],
            modelCosts: [
                {
                    model: "vision-text",
                    capability: "text",
                    protocol: "chat-completion",
                    billingMode: "fixed_request",
                    unitPriceMicrocredits: 1,
                    capabilityConfig: { version: 1, text: { contextWindowTokens: 128000, maxOutputTokens: 16384, references: { promptMaxChars: 32000, maxImages: 3, maxImageBytes: 30 * 1024 * 1024, maxVideos: 0, maxVideoBytes: 0 } } },
                },
            ],
        },
    ],
} as AiConfig;

describe("合规平台入口", () => {
    test("紧跟自由画布并使用独立路由", () => {
        const sidebar = source("../src/components/layout/workspace-sidebar-nav.tsx");
        expect(sidebar).toMatch(/toolItem\("canvas", "\/canvas"\)[^\n]*\n\s+toolItem\("compliance", "\/compliance"\)/);
        expect(navigationTools.find((item) => item.slug === "compliance")?.label).toBe("合规平台");
        expect(source("../src/components/layout/workspace-top-bar.tsx")).toContain('compliance: "合规平台"');
    });

    test("新页面保留认证边界和懒加载", () => {
        const router = source("../src/router.tsx");
        expect(router).toContain("const CompliancePage = lazy(loadCompliancePage)");
        expect(router).toContain('{ path: "/compliance", element: <RequireAuth>{deferred(<CompliancePage />)}</RequireAuth> }');
        expect(source("../src/lib/workspace-route-modules.ts")).toContain('compliance: () => import("@/pages/compliance")');
        expect(router).toContain('{ path: "/compliance/:id", element: <RequireAuth>{deferred(<CompliancePage />)}</RequireAuth> }');
    });

    test("空态只有核验输入框和能力卡片，没有结果", () => {
        const html = renderPage("/compliance");
        expect(html).toContain("<textarea");
        expect(html).toContain("两张图片对比");
        expect(html).toContain("网络批量对比");
        expect(html).toContain("即将上线");
        expect(html).not.toContain("核验结论");
    });

    test("核验记录加载前显示加载状态，不显示输入框", () => {
        const html = renderPage("/compliance/chk_1");
        expect(html).toContain("正在加载核验");
        expect(html).not.toContain("<textarea");
    });
});

describe("两张图片对比结果视图", () => {
    const render = (check: ComplianceCheck) => renderToStaticMarkup(<ComplianceFaceResult check={check} onNew={() => {}} />);

    test("完成后显示结论、小结、逐脸依据、建议和后端给出的判定阈值", () => {
        const html = render(baseCheck);
        expect(html).toContain("中等相似风险");
        expect(html).toContain("78%");
        expect(html).toContain("图片 A 检出 1 张人脸");
        expect(html).toContain("脸型与下颌线接近");
        expect(html).toContain("侧脸角度限制了判断");
        expect(html).toContain("换用一张清晰正脸图片后重新对比");
        expect(html).toContain("建议人工复核。");
        expect(html).toContain("存在疑似的人脸");
        expect(html).toContain("相似度 ≥ 85% 为高相似，70%–85% 为疑似相似，低于 70% 为低相似");
        expect(html).toContain("技术分析");
        expect(html).toContain("/resources/res_t/file");
        expect(html).not.toContain("演示数据");
    });

    test("风格化图像提示引擎相似度仅供参考", () => {
        const html = render({ ...baseCheck, result: { ...baseCheck.result!, style: "stylized" } });
        expect(html).toContain("风格化图像，引擎相似度仅供参考");
    });

    test("运行中按 Agent 实际做到的步骤显示进度，不显示结论与人脸框", () => {
        const html = render({ ...baseCheck, status: "running", steps: stepsOf(["done", "active", "pending", "pending"]), result: undefined });
        expect(html).toContain("Agent 正在对比");
        expect(html).toContain("查看图片");
        expect(html).toContain("检测人脸");
        expect(html).toContain('aria-current="step"');
        expect(html).not.toContain("核验结论");
        expect(html).not.toContain("逐脸明细");
        expect(html).not.toContain("核验小结");
    });

    test("失败时显示原因并保留素材，没有进行中的步骤", () => {
        const html = render({ ...baseCheck, status: "failed", steps: stepsOf(["done", "pending", "pending", "pending"]), result: undefined, error: "Agent 已结束但没有给出核验结论，请重新发起" });
        expect(html).toContain('role="alert"');
        expect(html).toContain("Agent 已结束但没有给出核验结论");
        expect(html).toContain("/resources/res_b/file");
        expect(html).not.toContain('aria-current="step"');
    });
});

describe("输入框提交方式", () => {
    test("没选能力也没放图片时，纯文字可以直接发给 Agent", () => {
        expect(resolveComposerSubmit({ skillId: null, hasTarget: false, baseCount: 0, prompt: "你好" })).toEqual({ mode: "chat", canSubmit: true, missing: [] });
        expect(resolveComposerSubmit({ skillId: null, hasTarget: false, baseCount: 0, prompt: "  " }).canSubmit).toBe(false);
    });

    test("选了两张图片对比或放了图片就是创建核验，素材不齐不能发，并说明缺什么", () => {
        expect(resolveComposerSubmit({ skillId: "face-compare", hasTarget: false, baseCount: 0, prompt: "你好" })).toEqual({ mode: "check", canSubmit: false, missing: ["图片 A", "图片 B"] });
        expect(resolveComposerSubmit({ skillId: null, hasTarget: true, baseCount: 0, prompt: "" })).toEqual({ mode: "check", canSubmit: false, missing: ["图片 B"] });
        expect(resolveComposerSubmit({ skillId: "face-compare", hasTarget: true, baseCount: 1, prompt: "" })).toEqual({ mode: "check", canSubmit: true, missing: [] });
    });
});

describe("承载核验的 Agent", () => {
    test("挑支持图片输入的系统渠道文本模型，页面不再替 Agent 拼首条消息", () => {
        expect(pickComplianceAgentModel(visionConfig)).toEqual({ channelId: "vision", channelModelKey: "vision-text" });
        expect(pickComplianceAgentModel({ ...visionConfig, model: "plain::plain-text" })).toEqual({ channelId: "vision", channelModelKey: "vision-text" });
        const page = source("../src/pages/compliance/index.tsx");
        expect(page).not.toContain("buildAgentPrompt");
        expect(page).toContain("agentModel");
    });

    test("没有支持图片输入的模型时在上传图片前就报错", () => {
        const noVision = { channels: [visionConfig.channels[0]] } as AiConfig;
        expect(() => pickComplianceAgentModel(noVision)).toThrow("支持图片输入");
    });

    test("页面上下文不占用画布上下文，复位时一并清空", () => {
        const store = useGlobalAgentStore.getState();
        store.setPage({ kind: "compliance", checkId: "c1" });
        expect(useGlobalAgentStore.getState()).toMatchObject({ canvas: null, page: { kind: "compliance", checkId: "c1" } });
        store.setPage({ kind: "compliance", checkId: null, launcherHidden: true });
        expect(useGlobalAgentStore.getState().page).toMatchObject({ checkId: null, launcherHidden: true });
        store.reset();
        expect(useGlobalAgentStore.getState().page).toBeNull();
    });

    test("面板把合规核验 ID 带进 Agent 请求，且不绑定画布", () => {
        const panel = source("../src/components/canvas/canvas-cloud-agent-panel.tsx");
        expect(panel).toContain('const complianceCheckId = useGlobalAgentStore((state) => state.page?.checkId ?? "")');
        expect(panel).toContain("...(complianceCheckId ? { complianceCheckId } : {})");
    });
});
