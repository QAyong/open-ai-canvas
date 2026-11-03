import { Button } from "antd";
import { useReducedMotion } from "motion/react";
import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router";

import { WorkspacePage } from "@/components/layout/workspace-page";
import { AGENT_DOCK_MIN_VIEWPORT } from "@/lib/canvas/agent-panel-dock";
import { useGlobalAgentStore } from "@/stores/use-global-agent-store";
import { ComplianceComposer } from "./compliance-composer";
import { buildAgentPrompt, type NewComplianceCheck } from "./compliance-check";
import { ComplianceFaceResult } from "./compliance-face-result";
import { useComplianceCheckStore } from "./compliance-check-store";

// Agent 目前还没有合规工具，也看不到素材图片：自动发送会白白消耗一次模型调用。
// 后端合规工具接上后改为 true，行为就和画布入口一致（提交即自动发出首条消息）。
const AUTO_SUBMIT_TO_AGENT = false;

/** 与 compliance-entry.css 里 .is-leaving 的 transform 时长一致。 */
const COMPOSER_LEAVE_MS = 320;

/** 空态：提交后输入框先飞向右下角，再创建核验并进入结果页。独立组件，回到空态时状态自然重置。 */
function ComplianceStart({ onStart }: { onStart: (input: NewComplianceCheck) => void }) {
    const reducedMotion = useReducedMotion();
    const [leaving, setLeaving] = useState(false);
    const timerRef = useRef<number | undefined>(undefined);
    useEffect(() => () => window.clearTimeout(timerRef.current), []);

    const submit = (input: NewComplianceCheck) => {
        if (reducedMotion) {
            onStart(input);
            return;
        }
        setLeaving(true);
        timerRef.current = window.setTimeout(() => onStart(input), COMPOSER_LEAVE_MS);
    };

    return (
        <WorkspacePage grid fluid scroll={false}>
            <div className="relative h-full">
                <ComplianceComposer leaving={leaving} onSubmit={submit} />
            </div>
        </WorkspacePage>
    );
}

export default function CompliancePage() {
    const { id } = useParams();
    const navigate = useNavigate();
    const check = useComplianceCheckStore((state) => (id ? state.checks[id] : undefined));
    const createCheck = useComplianceCheckStore((state) => state.create);
    const checkId = id ?? null;

    // 合规页内（空态、结果页）一直向平台 Agent 发布上下文：面板据此停靠，进出结果页不会掉成浮层。
    // 空态没有悬浮球，输入框就是入口。已有同一核验的上下文（提交时带着预填消息）就保留。
    useEffect(() => {
        const { page, setPage } = useGlobalAgentStore.getState();
        if (page?.checkId !== checkId) setPage({ kind: "compliance", checkId, launcherHidden: checkId === null });
        return () => {
            const current = useGlobalAgentStore.getState().page;
            if (current?.checkId === checkId) useGlobalAgentStore.getState().setPage(null);
        };
    }, [checkId]);

    const startCheck = (input: NewComplianceCheck) => {
        const newId = createCheck(input);
        const agent = useGlobalAgentStore.getState();
        agent.setPage({ kind: "compliance", checkId: newId, prefillRequest: { id: Date.now(), text: buildAgentPrompt(input), autoSubmit: AUTO_SUBMIT_TO_AGENT } });
        // 窄屏放不下停靠面板，浮层会盖住结果：只留悬浮球，由用户自己打开。
        if (window.innerWidth >= AGENT_DOCK_MIN_VIEWPORT) agent.openAgent();
        navigate(`/compliance/${newId}`);
    };

    if (id && !check) {
        return (
            <WorkspacePage>
                <div className="flex min-h-[60vh] flex-col items-center justify-center gap-3 text-center">
                    <p className="m-0 text-sm text-foreground/70">没有找到这次核验。演示数据只保存在当前标签页，刷新后会丢失。</p>
                    <Button onClick={() => navigate("/compliance")}>新的核验</Button>
                </div>
            </WorkspacePage>
        );
    }

    if (check) {
        return (
            <WorkspacePage>
                <ComplianceFaceResult check={check} onNew={() => navigate("/compliance")} />
            </WorkspacePage>
        );
    }

    return <ComplianceStart onStart={startCheck} />;
}
