import { useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button } from "antd";
import { Loader2, PanelRightClose, PanelRightOpen } from "lucide-react";
import { useReducedMotion } from "motion/react";
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from "react";
import { useNavigate, useParams } from "react-router";

import { WorkspacePage } from "@/components/layout/workspace-page";
import { useAgentPanelDock } from "@/components/canvas/use-agent-panel-dock";
import { AGENT_DOCK_MIN_VIEWPORT } from "@/lib/canvas/agent-panel-dock";
import { createComplianceCheck, getComplianceCheck, type ComplianceAgentModel, type ComplianceCheck } from "@/services/api/compliance";
import { uploadResourceFile } from "@/services/api/resources";
import { useEffectiveConfig } from "@/stores/use-config-store";
import { useGlobalAgentStore } from "@/stores/use-global-agent-store";
import { ComplianceComposer, type ComplianceSubmitInput } from "./compliance-composer";
import { pickComplianceAgentModel } from "./compliance-check";
import { ComplianceFaceResult } from "./compliance-face-result";
import "./compliance-workbench.css";

/** 与 compliance-entry.css 里 .is-leaving 的 transform 时长一致。 */
const COMPOSER_LEAVE_MS = 320;

/** 核验运行中按这个间隔刷新进度；步骤随 Agent 的工具调用推进。 */
const POLL_MS = 800;

const checkQueryKey = (id: string) => ["compliance-check", id] as const;

/**
 * 先把选好的图片上传成资源，再创建核验；核验创建后由服务端发起 Agent 运行，按合规技能调用比对工具。
 * 上传到一半失败时已上传的资源留在素材库，不会被核验引用。
 */
async function createCheckFromPicked(input: ComplianceSubmitInput, agentModel: ComplianceAgentModel) {
    const [target, ...bases] = await Promise.all([input.target, ...input.bases].map((image) => uploadResourceFile(image.file, "image")));
    return createComplianceCheck({
        kind: "face_compare",
        prompt: input.prompt,
        subjectName: input.subjectName,
        target: { resourceId: target.id, name: input.target.name },
        bases: bases.map((resource, index) => ({ resourceId: resource.id, name: input.bases[index].name })),
        agentModel,
    });
}

/** 空态：上传并创建成功后，输入框飞向右下角，再进入结果页。独立组件，回到空态时状态自然重置。 */
function ComplianceStart({ onCreate, onEnter, onChat }: { onCreate: (input: ComplianceSubmitInput) => Promise<ComplianceCheck>; onEnter: (check: ComplianceCheck) => void; onChat: (text: string) => void }) {
    const { message } = App.useApp();
    const reducedMotion = useReducedMotion();
    const [submitting, setSubmitting] = useState(false);
    const [leaving, setLeaving] = useState(false);
    const timerRef = useRef<number | undefined>(undefined);
    useEffect(() => () => window.clearTimeout(timerRef.current), []);

    const submit = async (input: ComplianceSubmitInput) => {
        setSubmitting(true);
        try {
            const check = await onCreate(input);
            if (reducedMotion) {
                onEnter(check);
                return;
            }
            setLeaving(true);
            timerRef.current = window.setTimeout(() => onEnter(check), COMPOSER_LEAVE_MS);
        } catch (error) {
            message.error(error instanceof Error && error.message ? error.message : "提交失败，请稍后重试");
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <WorkspacePage grid fluid scroll={false}>
            <div className="relative h-full">
                <ComplianceComposer submitting={submitting} leaving={leaving} onSubmit={submit} onChat={onChat} />
            </div>
        </WorkspacePage>
    );
}

export default function CompliancePage() {
    const { id } = useParams();
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const config = useEffectiveConfig();
    const checkId = id ?? null;
    const [panelHost, setPanelHost] = useState<HTMLDivElement | null>(null);
    const agentOpen = useGlobalAgentStore((state) => state.open);
    const agentDock = useAgentPanelDock();
    const checkQuery = useQuery({
        queryKey: checkQueryKey(id ?? ""),
        queryFn: ({ signal }) => getComplianceCheck(id!, signal),
        enabled: Boolean(id),
        retry: false,
        refetchInterval: (query) => (query.state.data?.status === "running" ? POLL_MS : false),
    });

    const hasReport = Boolean(id && checkQuery.data?.status === "done" && checkQuery.data.result);

    // 离开时恢复原来的开合状态，不修改画布的停靠偏好。
    useLayoutEffect(() => {
        const agent = useGlobalAgentStore.getState();
        const wasOpen = agent.open;
        return () => {
            const current = useGlobalAgentStore.getState();
            if (wasOpen) current.openAgent();
            else current.closeAgent();
        };
    }, []);

    // 初始输入和执行进度保持单栏；只有真实报告保存完成后才展开两栏。
    useLayoutEffect(() => {
        const agent = useGlobalAgentStore.getState();
        if (hasReport && window.innerWidth >= AGENT_DOCK_MIN_VIEWPORT) agent.openAgent();
        else agent.closeAgent();
    }, [hasReport]);

    useLayoutEffect(() => {
        const { page, setPage } = useGlobalAgentStore.getState();
        const host = hasReport ? panelHost : null;
        setPage({ ...(page?.checkId === checkId ? page : {}), kind: "compliance", checkId, panelHost: host, launcherHidden: hasReport || checkId === null });
        return () => {
            const current = useGlobalAgentStore.getState().page;
            if (current?.checkId === checkId && current.panelHost === host) useGlobalAgentStore.getState().setPage(null);
        };
    }, [checkId, panelHost, hasReport]);

    // 没有素材的纯文字直接发给 Agent 对话：不创建核验，页面停在空态，面板打开后由用户在里面继续。
    // 尚未产出报告时沿用原来的对话面板，不提前切换为报告两栏。
    const chatWithAgent = (text: string) => {
        const agent = useGlobalAgentStore.getState();
        agent.setPage({ kind: "compliance", checkId: null, panelHost, launcherHidden: true, prefillRequest: { id: Date.now(), text, autoSubmit: true } });
        agent.openAgent();
    };

    // Agent 运行已由服务端发起；右栏沿用当前面板，左栏进入核验结果。
    const enterCheck = (check: ComplianceCheck) => {
        queryClient.setQueryData(checkQueryKey(check.id), check);
        useGlobalAgentStore.getState().setPage({ kind: "compliance", checkId: check.id, panelHost, launcherHidden: true });
        navigate(`/compliance/${check.id}`);
    };

    const renderReport = () => {
        if (!id) return <ComplianceStart onCreate={(input) => createCheckFromPicked(input, pickComplianceAgentModel(config))} onEnter={enterCheck} onChat={chatWithAgent} />;

        if (checkQuery.isPending) {
            return (
                <WorkspacePage>
                    <div className="flex min-h-[60vh] items-center justify-center text-foreground/60" role="status">
                        <Loader2 className="size-5 animate-spin" aria-label="正在加载核验" />
                    </div>
                </WorkspacePage>
            );
        }

        if (checkQuery.isError) {
            return (
                <WorkspacePage>
                    <div className="flex min-h-[60vh] flex-col items-center justify-center gap-3 text-center">
                        <p className="m-0 text-sm text-foreground/70">没有找到这次核验，或者它不属于当前账号。</p>
                        <Button onClick={() => navigate("/compliance")}>新的核验</Button>
                    </div>
                </WorkspacePage>
            );
        }

        return (
            <WorkspacePage>
                <ComplianceFaceResult
                    check={checkQuery.data}
                    onNew={() => navigate("/compliance")}
                    actions={
                        hasReport ? (
                            <Button
                                icon={agentOpen ? <PanelRightClose className="size-4" /> : <PanelRightOpen className="size-4" />}
                                aria-expanded={agentOpen}
                                aria-controls="compliance-agent-column"
                                onClick={() => {
                                    const agent = useGlobalAgentStore.getState();
                                    if (agent.open) agent.closeAgent();
                                    else agent.openAgent();
                                }}
                            >
                                {agentOpen ? "收起 AI 对话" : "AI 对话"}
                            </Button>
                        ) : undefined
                    }
                />
            </WorkspacePage>
        );
    };

    if (!hasReport) return renderReport();

    return (
        <section className="compliance-workbench" aria-label="合规工作区" data-agent-open={agentOpen} style={{ "--compliance-agent-width": `${agentDock.width}px` } as CSSProperties}>
            <div className="compliance-workbench-columns">
                <div className="compliance-report-column" aria-label="合规报告">
                    {renderReport()}
                </div>
                <div id="compliance-agent-column" className="compliance-agent-column" ref={setPanelHost} />
            </div>
        </section>
    );
}
