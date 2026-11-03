import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter, Route, Routes } from "react-router";

import { navigationTools } from "../src/constant/navigation-tools";
import CompliancePage from "../src/pages/compliance";
import { buildAgentPrompt, complianceSteps, FACE_MATCH_THRESHOLD, FACE_SUSPECT_THRESHOLD, faceVerdictOf, overallVerdictOf } from "../src/pages/compliance/compliance-check";
import { useComplianceCheckStore } from "../src/pages/compliance/compliance-check-store";
import { useGlobalAgentStore } from "../src/stores/use-global-agent-store";

const source = (path: string) => readFileSync(resolve(import.meta.dir, path), "utf8");

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
        const html = renderToStaticMarkup(
            <MemoryRouter initialEntries={["/compliance"]}>
                <CompliancePage />
            </MemoryRouter>,
        );
        expect(html).toContain("<textarea");
        expect(html).toContain("人脸比对");
        expect(html).toContain("即将上线");
        expect(html).not.toContain("核验结论");
    });

    test("找不到核验记录时给出回到空态的入口", () => {
        const html = renderToStaticMarkup(
            <MemoryRouter initialEntries={["/compliance/missing"]}>
                <Routes>
                    <Route path="/compliance/:id" element={<CompliancePage />} />
                </Routes>
            </MemoryRouter>,
        );
        expect(html).toContain("没有找到这次核验");
        expect(html).not.toContain("<textarea");
    });
});

describe("人脸比对演示判定", () => {
    const face = (similarity: number) => ({ index: 1, box: { x: 0, y: 0, w: 1, h: 1 }, similarity, bestBaseIndex: 0, verdict: faceVerdictOf(similarity) });

    test("相似度按阈值分档", () => {
        expect(faceVerdictOf(FACE_MATCH_THRESHOLD)).toBe("match");
        expect(faceVerdictOf(FACE_MATCH_THRESHOLD - 1)).toBe("suspect");
        expect(faceVerdictOf(FACE_SUSPECT_THRESHOLD)).toBe("suspect");
        expect(faceVerdictOf(FACE_SUSPECT_THRESHOLD - 1)).toBe("mismatch");
    });

    test("多出未匹配的人脸时转人工复核，不直接通过", () => {
        expect(overallVerdictOf([])).toBe("no_face");
        expect(overallVerdictOf([face(95)])).toBe("pass");
        expect(overallVerdictOf([face(95), face(40)])).toBe("review");
        expect(overallVerdictOf([face(75)])).toBe("review");
        expect(overallVerdictOf([face(40)])).toBe("risk");
    });
});

describe("核验进度与 Agent 上下文", () => {
    const image = (name: string) => ({ url: `blob:${name}`, name });
    const input = { skillId: "face-compare" as const, prompt: "", subjectName: "张三", target: image("still.png"), bases: [image("a.png"), image("b.png")] };

    test("核验按步骤推进，最后一步完成才算结束", async () => {
        const id = useComplianceCheckStore.getState().create(input, 5);
        expect(useComplianceCheckStore.getState().checks[id]).toMatchObject({ status: "running", stepsDone: 0 });
        await Bun.sleep(5 * complianceSteps.length + 60);
        expect(useComplianceCheckStore.getState().checks[id]).toMatchObject({ status: "done", stepsDone: complianceSteps.length });
    });

    test("发给 Agent 的首条消息带上素材清单", () => {
        expect(buildAgentPrompt(input)).toBe("请帮我核验这次人脸比对。（待核验素材：still.png；授权底图 2 张；授权人：张三）");
        expect(buildAgentPrompt({ ...input, prompt: "这是演员本人吗？", subjectName: "" })).toBe("这是演员本人吗？（待核验素材：still.png；授权底图 2 张）");
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
});
