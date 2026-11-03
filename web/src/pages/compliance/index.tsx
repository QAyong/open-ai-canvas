import { useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button } from "antd";
import { Loader2 } from "lucide-react";
import { useReducedMotion } from "motion/react";
import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router";

import { WorkspacePage } from "@/components/layout/workspace-page";
import { AGENT_DOCK_MIN_VIEWPORT } from "@/lib/canvas/agent-panel-dock";
import { createComplianceCheck, getComplianceCheck, type ComplianceCheck } from "@/services/api/compliance";
import { uploadResourceFile } from "@/services/api/resources";
import { useGlobalAgentStore } from "@/stores/use-global-agent-store";
import { ComplianceComposer, type ComplianceSubmitInput } from "./compliance-composer";
import { buildAgentPrompt } from "./compliance-check";
import { ComplianceFaceResult } from "./compliance-face-result";

// 提交即自动把首条消息发给 Agent（和画布入口一致）；Agent 用 compliance_get_check 读取结果后解读。
const AUTO_SUBMIT_TO_AGENT = true;

/** 与 compliance-entry.css 里 .is-leaving 的 transform 时长一致。 */
const COMPOSER_LEAVE_MS = 320;

/** 核验运行中按这个间隔刷新进度。 */
const POLL_MS = 800;

const checkQueryKey = (id: string) => ["compliance-check", id] as const;

/** 先把选好的图片上传成资源，再创建核验。上传到一半失败时已上传的资源留在素材库，不会被核验引用。 */
async function createCheckFromPicked(input: ComplianceSubmitInput) {
    const [target, ...bases] = await Promise.all([input.target, ...input.bases].map((image) => uploadResourceFile(image.file, "image")));
    return createComplianceCheck({
        kind: "face_compare",
        prompt: input.prompt,
        subjectName: input.subjectName,
        target: { resourceId: target.id, name: input.target.name },
        bases: bases.map((resource, index) => ({ resourceId: resource.id, name: input.bases[index].name })),
    });
}

/** 空态：上传并创建成功后，输入框飞向右下角，再进入结果页。独立组件，回到空态时状态自然重置。 */
function ComplianceStart({ onCreate, onEnter, onChat }: { onCreate: (input: ComplianceSubmitInput) => Promise<ComplianceCheck>; onEnter: (check: ComplianceCheck, input: ComplianceSubmitInput) => void; onChat: (text: string) => void }) {
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
                onEnter(check, input);
                return;
            }
            setLeaving(true);
            timerRef.current = window.setTimeout(() => onEnter(check, input), COMPOSER_LEAVE_MS);
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
    const checkId = id ?? null;
    const checkQuery = useQuery({
        queryKey: checkQueryKey(id ?? ""),
        queryFn: ({ signal }) => getComplianceCheck(id!, signal),
        enabled: Boolean(id),
        retry: false,
        refetchInterval: (query) => (query.state.data?.status === "running" ? POLL_MS : false),
    });

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

    // 没有素材的纯文字直接发给 Agent 对话：不创建核验，页面停在空态，面板打开后由用户在里面继续。
    // 用户明确发了一条消息，窄屏也打开面板（浮层）；空态没有悬浮球，不打开就看不到回复。
    const chatWithAgent = (text: string) => {
        const agent = useGlobalAgentStore.getState();
        agent.setPage({ kind: "compliance", checkId: null, launcherHidden: true, prefillRequest: { id: Date.now(), text, autoSubmit: true } });
        agent.openAgent();
    };

    const enterCheck = (check: ComplianceCheck, input: ComplianceSubmitInput) => {
        queryClient.setQueryData(checkQueryKey(check.id), check);
        const agent = useGlobalAgentStore.getState();
        const text = buildAgentPrompt({ prompt: input.prompt, subjectName: input.subjectName, targetName: input.target.name, baseCount: input.bases.length });
        agent.setPage({ kind: "compliance", checkId: check.id, prefillRequest: { id: Date.now(), text, autoSubmit: AUTO_SUBMIT_TO_AGENT } });
        // 窄屏放不下停靠面板，浮层会盖住结果：只留悬浮球，由用户自己打开。
        if (window.innerWidth >= AGENT_DOCK_MIN_VIEWPORT) agent.openAgent();
        navigate(`/compliance/${check.id}`);
    };

    if (!id) return <ComplianceStart onCreate={createCheckFromPicked} onEnter={enterCheck} onChat={chatWithAgent} />;

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
            <ComplianceFaceResult check={checkQuery.data} onNew={() => navigate("/compliance")} />
        </WorkspacePage>
    );
}
