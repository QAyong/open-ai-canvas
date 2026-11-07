import { afterEach, describe, expect, test } from "bun:test";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter, Route, Routes } from "react-router";

import CompliancePage from "../src/pages/compliance";
import type { ComplianceCheck } from "../src/services/api/compliance";
import { useGlobalAgentStore } from "../src/stores/use-global-agent-store";

const completedCheck: ComplianceCheck = {
    id: "check-layout",
    kind: "face_compare",
    prompt: "",
    subjectName: "布局测试",
    status: "done",
    steps: [],
    runId: "layout-run",
    skillVersion: "test",
    thresholds: { match: 85, suspect: 70 },
    target: { resourceId: "image-a", name: "A.png" },
    bases: [{ resourceId: "image-b", name: "B.png" }],
    result: {
        overall: "review",
        style: "realistic",
        summary: "测试报告内容",
        faces: [],
        suggestions: [],
        humanReview: { required: false, reason: "" },
        disclaimer: "仅用于界面测试",
    },
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
};

function renderPage(path = "/compliance", check?: ComplianceCheck) {
    const client = new QueryClient();
    if (check) client.setQueryData(["compliance-check", check.id], check);
    return renderToStaticMarkup(
        <QueryClientProvider client={client}>
            <MemoryRouter initialEntries={[path]}>
                <Routes>
                    <Route path="/compliance" element={<CompliancePage />} />
                    <Route path="/compliance/:id" element={<CompliancePage />} />
                </Routes>
            </MemoryRouter>
        </QueryClientProvider>,
    );
}

const source = (path: string) => readFileSync(resolve(import.meta.dir, path), "utf8");
afterEach(() => useGlobalAgentStore.getState().reset());

describe("合规工作区两栏", () => {
    test("尚未对话时保留原来的完整输入界面，不显示两栏或收起按钮", () => {
        const html = renderPage();
        expect(html).toContain("<textarea");
        expect(html).toContain("两张图片对比");
        expect(html).not.toContain('class="compliance-workbench"');
        expect(html).not.toContain('id="compliance-agent-column"');
        expect(html).not.toContain("展开 AI 对话");
    });

    test("加载、执行中、失败以及缺少报告时都不提前切换两栏", () => {
        const loading = renderPage("/compliance/check-layout");
        expect(loading).toContain("正在加载核验");
        expect(loading).not.toContain('id="compliance-agent-column"');
        for (const status of ["running", "failed", "done"] as const) {
            const html = renderPage("/compliance/check-layout", { ...completedCheck, status, result: undefined });
            expect(html).not.toContain('class="compliance-workbench"');
            expect(html).not.toContain('id="compliance-agent-column"');
        }
    });

    test("Skill 完成并保存报告后才提供报告和 AI 两栏，打开历史报告也一样", () => {
        const html = renderPage("/compliance/check-layout", completedCheck);
        expect(html).toContain('aria-label="合规工作区"');
        expect(html).toContain('class="compliance-report-column"');
        expect(html).toContain('id="compliance-agent-column"');
        expect(html).toContain("测试报告内容");
        expect(html).toContain('aria-controls="compliance-agent-column"');
        expect(html).not.toContain("compliance-workbench-toolbar");
        expect(html).not.toContain("左侧查看报告，右侧与 AI 对话");
        expect(html).not.toContain("<textarea");
    });

    test("面板开合不修改画布停靠偏好，返回新对比时恢复单栏入口", () => {
        const dockPreference = useGlobalAgentStore.getState().dock;
        useGlobalAgentStore.getState().openAgent();
        expect(useGlobalAgentStore.getState().open).toBe(true);
        expect(useGlobalAgentStore.getState().dock).toBe(dockPreference);
        useGlobalAgentStore.getState().closeAgent();
        expect(useGlobalAgentStore.getState().open).toBe(false);
        expect(useGlobalAgentStore.getState().dock).toBe(dockPreference);
        const html = renderPage();
        expect(html).toContain("<textarea");
        expect(html).not.toContain('id="compliance-agent-column"');
    });

    test("平台唯一 Agent 挂入结果页，外层不重复让宽或提供浮动切换", () => {
        const panel = source("../src/components/canvas/canvas-cloud-agent-panel.tsx");
        expect(panel).toContain("createPortal(panelContent, panelHost)");
        expect(panel).toContain('...(panelHost ? { inset: "auto", width: "100%", height: "100%" } : {})');
        expect(panel).toContain("dock.available && !panelHost");
        expect(source("../src/layouts/user-layout.tsx")).toContain("agentDock.active && !agentDock.embedded");
        expect(source("../src/components/canvas/use-agent-panel-dock.ts")).toContain("state.canvas !== null || Boolean(state.page?.panelHost)");
    });

    test("只在已产出报告的合规页隐藏平台顶栏，初始页和其他路由保留", () => {
        const shell = source("../src/components/layout/app-top-nav.tsx");
        expect(shell).toContain('pathname.startsWith("/compliance/") && hasComplianceReport');
        expect(shell).toContain("const showGlobalTopBar = !hideChrome && !complianceReportWorkspace");
        expect(shell).toContain("Boolean(state.page?.panelHost)");
    });

    test("移动端结果页切换视图而非叠加浮窗", () => {
        const css = source("../src/pages/compliance/compliance-workbench.css");
        expect(css).toContain("grid-template-columns: minmax(0, 1fr) var(--compliance-agent-width)");
        expect(css).toContain("@media (max-width: 959px)");
        expect(css).toContain('.compliance-workbench[data-agent-open="true"] .compliance-report-column');
        expect(css).not.toContain("position: fixed");
    });
});
