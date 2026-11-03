import { describe, expect, test } from "bun:test";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter, Route, Routes } from "react-router";

import { navigationTools } from "../src/constant/navigation-tools";
import CompliancePage from "../src/pages/compliance";
import { ComplianceFaceResult } from "../src/pages/compliance/compliance-face-result";
import { buildAgentPrompt, resolveComposerSubmit } from "../src/pages/compliance/compliance-check";
import type { ComplianceCheck } from "../src/services/api/compliance";
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

const baseCheck: ComplianceCheck = {
    id: "chk_1",
    kind: "face_compare",
    prompt: "",
    subjectName: "张三",
    status: "done",
    stepsDone: 3,
    stepCount: 3,
    thresholds: { match: 85, suspect: 70 },
    target: { resourceId: "res_t", name: "still.png" },
    bases: [{ resourceId: "res_b", name: "base.png" }],
    result: { overall: "review", faces: [{ index: 1, box: { x: 10, y: 10, w: 30, h: 40 }, similarity: 78, bestBaseIndex: 0, verdict: "suspect" }] },
    createdAt: "2026-11-03T10:00:00Z",
    updatedAt: "2026-11-03T10:00:03Z",
};

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
        expect(html).toContain("人脸比对");
        expect(html).toContain("即将上线");
        expect(html).not.toContain("核验结论");
    });

    test("核验记录加载前显示加载状态，不显示输入框", () => {
        const html = renderPage("/compliance/chk_1");
        expect(html).toContain("正在加载核验");
        expect(html).not.toContain("<textarea");
    });
});

describe("人脸比对结果视图", () => {
    const render = (check: ComplianceCheck) => renderToStaticMarkup(<ComplianceFaceResult check={check} onNew={() => {}} />);

    test("完成后显示结论、人脸明细和后端给出的判定阈值", () => {
        const html = render(baseCheck);
        expect(html).toContain("建议复核");
        expect(html).toContain("78%");
        expect(html).toContain("相似度 ≥ 85% 为一致，70%–85% 为疑似，低于 70% 为不一致");
        expect(html).toContain("/resources/res_t/file");
        expect(html).not.toContain("演示数据");
    });

    test("运行中只显示素材和进度，不显示结论与人脸框", () => {
        const html = render({ ...baseCheck, status: "running", stepsDone: 1, result: undefined });
        expect(html).toContain("正在检测并比对");
        expect(html).not.toContain("核验结论");
        expect(html).not.toContain("逐脸明细");
    });

    test("失败时显示原因并保留素材", () => {
        const html = render({ ...baseCheck, status: "failed", stepsDone: 0, result: undefined, error: "核验失败，请稍后重试" });
        expect(html).toContain('role="alert"');
        expect(html).toContain("核验失败，请稍后重试");
        expect(html).toContain("/resources/res_b/file");
    });
});

describe("输入框提交方式", () => {
    test("没选能力也没放图片时，纯文字可以直接发给 Agent", () => {
        expect(resolveComposerSubmit({ skillId: null, hasTarget: false, baseCount: 0, prompt: "你好" })).toEqual({ mode: "chat", canSubmit: true, missing: [] });
        expect(resolveComposerSubmit({ skillId: null, hasTarget: false, baseCount: 0, prompt: "  " }).canSubmit).toBe(false);
    });

    test("选了人脸比对或放了图片就是创建核验，素材不齐不能发，并说明缺什么", () => {
        expect(resolveComposerSubmit({ skillId: "face-compare", hasTarget: false, baseCount: 0, prompt: "你好" })).toEqual({ mode: "check", canSubmit: false, missing: ["待核验素材", "授权底图"] });
        expect(resolveComposerSubmit({ skillId: null, hasTarget: true, baseCount: 0, prompt: "" })).toEqual({ mode: "check", canSubmit: false, missing: ["授权底图"] });
        expect(resolveComposerSubmit({ skillId: "face-compare", hasTarget: true, baseCount: 2, prompt: "" })).toEqual({ mode: "check", canSubmit: true, missing: [] });
    });
});

describe("Agent 上下文", () => {
    test("发给 Agent 的首条消息带上素材清单，没有描述时让它先读结果", () => {
        expect(buildAgentPrompt({ prompt: "", subjectName: "张三", targetName: "still.png", baseCount: 2 })).toBe("请先读取核验结果，再解读并给出处置建议。（待核验素材：still.png；授权底图 2 张；授权人：张三）");
        expect(buildAgentPrompt({ prompt: "这是演员本人吗？", subjectName: "", targetName: "still.png", baseCount: 1 })).toBe("这是演员本人吗？（待核验素材：still.png；授权底图 1 张）");
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
