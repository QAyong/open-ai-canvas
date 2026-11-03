// 人脸比对结果视图：步骤条 + 待核验素材（人脸框标注）与授权底图并排 + 逐脸明细。
// 结果随 check.stepsDone 逐块出现：素材和底图立即可见，人脸框、相似度、结论依次出现。

import { Button } from "antd";
import { Bot, Check, Loader2, Plus } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import type { ReactNode } from "react";

import { StatusBadge } from "@/components/ui/base/badges/status-badge";
import { aceternityMotion } from "@/lib/aceternity-motion";
import { cn } from "@/lib/utils";
import { useGlobalAgentStore } from "@/stores/use-global-agent-store";
import { complianceSteps, FACE_MATCH_THRESHOLD, FACE_SUSPECT_THRESHOLD, type ComplianceCheck, type FaceVerdict, type OverallVerdict } from "./compliance-check";

type Tone = "success" | "warning" | "error" | "neutral";

const overallMeta: Record<OverallVerdict, { label: string; tone: Tone; summary: string }> = {
    pass: { label: "核验通过", tone: "success", summary: "素材中的人脸与授权底图一致。" },
    review: { label: "建议复核", tone: "warning", summary: "存在疑似或未能匹配授权人的人脸，建议人工确认后再使用。" },
    risk: { label: "存在风险", tone: "error", summary: "素材中的人脸与授权底图不一致，可能涉及未授权使用肖像。" },
    no_face: { label: "未检出人脸", tone: "neutral", summary: "没有在素材里检出人脸，请换一张清晰的正面图重试。" },
};

const faceMeta: Record<FaceVerdict, { label: string; tone: Tone; box: string; bar: string }> = {
    match: { label: "一致", tone: "success", box: "border-status-success", bar: "bg-status-success" },
    suspect: { label: "疑似", tone: "warning", box: "border-status-warning", bar: "bg-status-warning" },
    mismatch: { label: "不一致", tone: "error", box: "border-status-error", bar: "bg-status-error" },
};

function MockTag() {
    return <span className="rounded-full bg-foreground/8 px-2 py-0.5 text-tiny text-foreground/60">演示数据</span>;
}

/** 内容出现时淡入上移；减少动效时直接显示。 */
function Reveal({ children, className }: { children: ReactNode; className?: string }) {
    const reducedMotion = useReducedMotion();
    return (
        <motion.div initial={reducedMotion ? false : { opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} transition={aceternityMotion.spring.surface} className={className}>
            {children}
        </motion.div>
    );
}

function Panel({ title, children, className }: { title: string; children: ReactNode; className?: string }) {
    return (
        <section className={cn("rounded-xl border border-border bg-card p-4", className)}>
            <h2 className="mb-3 text-sm font-medium text-foreground">{title}</h2>
            {children}
        </section>
    );
}

function Steps({ stepsDone }: { stepsDone: number }) {
    return (
        <ol className="m-0 flex list-none flex-wrap items-center gap-x-2 gap-y-2 p-0" aria-label="核验进度">
            {complianceSteps.map((label, index) => {
                const done = index < stepsDone;
                const current = index === stepsDone;
                return (
                    <li key={label} className="flex items-center gap-2" aria-current={current ? "step" : undefined}>
                        {index ? <span aria-hidden="true" className={cn("h-px w-6 sm:w-10", done || current ? "bg-foreground/40" : "bg-foreground/15")} /> : null}
                        <span
                            className={cn(
                                "grid size-5 place-items-center rounded-full text-tiny font-semibold",
                                done ? "bg-foreground text-background" : current ? "border border-foreground/60 text-foreground" : "border border-foreground/20 text-foreground/40",
                            )}
                        >
                            {done ? <Check className="size-3" aria-hidden="true" /> : current ? <Loader2 className="size-3 animate-spin" aria-hidden="true" /> : index + 1}
                        </span>
                        <span className={cn("text-caption", done || current ? "font-medium text-foreground" : "text-foreground/45")}>{label}</span>
                    </li>
                );
            })}
        </ol>
    );
}

export function ComplianceFaceResult({ check, onNew }: { check: ComplianceCheck; onNew: () => void }) {
    const result = check.result;
    const detected = check.stepsDone >= 1;
    const compared = check.stepsDone >= 2;
    const concluded = check.stepsDone >= complianceSteps.length;
    const overall = result ? overallMeta[result.overall] : null;
    const best = result?.faces.length ? Math.max(...result.faces.map((face) => face.similarity)) : null;
    const title = check.subjectName ? `人脸比对 · ${check.subjectName}` : "人脸比对";

    return (
        <div className="mx-auto flex w-full max-w-[1080px] flex-col gap-4 pb-28">
            <header className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex min-w-0 items-center gap-2">
                    <h1 className="truncate text-lg font-semibold text-foreground">{title}</h1>
                    <MockTag />
                </div>
                <Button icon={<Plus className="size-4" />} onClick={onNew}>
                    新的核验
                </Button>
            </header>

            <Steps stepsDone={check.stepsDone} />

            {concluded && overall ? (
                <Reveal>
                    <section className="flex flex-wrap items-center gap-x-6 gap-y-3 rounded-xl border border-border bg-card px-5 py-4" aria-label="核验结论">
                        <StatusBadge variant="filled" tone={overall.tone} label={overall.label} />
                        {best === null ? null : (
                            <div className="flex items-baseline gap-1">
                                <span className="text-4xl font-semibold tabular-nums text-foreground">{best}</span>
                                <span className="text-sm text-foreground/60">% 最高相似度</span>
                            </div>
                        )}
                        <p className="m-0 min-w-[240px] flex-1 text-sm leading-6 text-foreground/70">{overall.summary}</p>
                    </section>
                </Reveal>
            ) : null}

            <div className="grid gap-4 md:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
                <Panel title="待核验素材">
                    <div className="relative overflow-hidden rounded-lg bg-muted">
                        <img src={check.target.url} alt={check.target.name} className="block max-h-[520px] w-full object-contain" />
                        {detected && result
                            ? result.faces.map((face) => (
                                  <motion.div
                                      key={face.index}
                                      initial={{ opacity: 0 }}
                                      animate={{ opacity: 1 }}
                                      className={cn("absolute rounded-sm border-2", compared ? faceMeta[face.verdict].box : "border-foreground/70")}
                                      style={{ left: `${face.box.x}%`, top: `${face.box.y}%`, width: `${face.box.w}%`, height: `${face.box.h}%` }}
                                  >
                                      <span className="absolute -top-px left-0 -translate-y-full rounded-t-sm bg-background/90 px-1.5 text-tiny font-medium text-foreground">#{face.index}</span>
                                  </motion.div>
                              ))
                            : null}
                        {detected ? null : (
                            <div className="absolute inset-0 grid place-items-center bg-background/40" role="status">
                                <span className="inline-flex items-center gap-2 rounded-full bg-background/90 px-3 py-1 text-caption text-foreground/70">
                                    <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                                    正在检测人脸…
                                </span>
                            </div>
                        )}
                    </div>
                </Panel>
                <Panel title={`授权底图（${check.bases.length}）`}>
                    <div className="grid grid-cols-2 gap-2">
                        {check.bases.map((base, index) => (
                            <figure key={base.url} className="m-0">
                                <img src={base.url} alt={base.name} className="aspect-square w-full rounded-lg bg-muted object-cover" />
                                <figcaption className="mt-1 truncate text-tiny text-foreground/55">底图 {index + 1}</figcaption>
                            </figure>
                        ))}
                    </div>
                </Panel>
            </div>

            {detected && result ? (
                <Reveal>
                    <Panel title="逐脸明细">
                        {result.faces.length ? (
                            <ul className="m-0 flex list-none flex-col gap-3 p-0">
                                {result.faces.map((face) => (
                                    <li key={face.index} className="grid grid-cols-[2.5rem_minmax(0,1fr)_auto] items-center gap-3">
                                        <span className="text-sm font-medium text-foreground">#{face.index}</span>
                                        {compared ? (
                                            <>
                                                <div className="min-w-0">
                                                    <div className="mb-1 flex justify-between text-caption text-foreground/60">
                                                        <span>最接近底图 {face.bestBaseIndex + 1}</span>
                                                        <span className="tabular-nums">{face.similarity}%</span>
                                                    </div>
                                                    <div className="h-1.5 overflow-hidden rounded-full bg-foreground/10" role="progressbar" aria-valuenow={face.similarity} aria-valuemin={0} aria-valuemax={100} aria-label={`人脸 ${face.index} 相似度`}>
                                                        <div className={cn("h-full rounded-full", faceMeta[face.verdict].bar)} style={{ width: `${face.similarity}%` }} />
                                                    </div>
                                                </div>
                                                <StatusBadge variant="filled" size="sm" tone={faceMeta[face.verdict].tone} label={faceMeta[face.verdict].label} />
                                            </>
                                        ) : (
                                            <span className="col-span-2 inline-flex items-center gap-2 text-caption text-foreground/55" role="status">
                                                <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                                                正在与授权底图比对…
                                            </span>
                                        )}
                                    </li>
                                ))}
                            </ul>
                        ) : (
                            <p className="m-0 text-sm text-foreground/60">没有检出人脸。</p>
                        )}
                        {concluded ? (
                            <p className="mb-0 mt-4 text-caption leading-5 text-foreground/50">
                                判定规则：相似度 ≥ {FACE_MATCH_THRESHOLD}% 为一致，{FACE_SUSPECT_THRESHOLD}%–{FACE_MATCH_THRESHOLD}% 为疑似，低于 {FACE_SUSPECT_THRESHOLD}% 为不一致。结论只来自比对引擎的数值，AI 负责解读，不参与判定。
                            </p>
                        ) : null}
                    </Panel>
                </Reveal>
            ) : null}

            <Panel title="AI 解读">
                <div className="flex flex-wrap items-center justify-between gap-3">
                    <p className="m-0 min-w-[240px] flex-1 text-sm leading-6 text-foreground/70">接入合规工具后，这里由 AI 结合合规技能解读结果、给出处置建议。对话在右侧 Agent 面板里进行。</p>
                    <Button icon={<Bot className="size-4" />} onClick={() => useGlobalAgentStore.getState().openAgent()}>
                        向 AI 追问
                    </Button>
                </div>
            </Panel>
        </div>
    );
}
