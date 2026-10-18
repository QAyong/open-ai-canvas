import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { renderToStaticMarkup } from "react-dom/server";

import { navigationTools } from "../src/constant/navigation-tools";
import CompliancePage from "../src/pages/compliance";

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
    });

    test("只渲染工作台空白页", () => {
        const html = renderToStaticMarkup(<CompliancePage />);
        expect(html).toContain("<main");
        expect(html.replace(/<[^>]+>/g, "").trim()).toBe("");
        expect(html).not.toContain("<input");
        expect(html).not.toContain("<button");
    });
});
