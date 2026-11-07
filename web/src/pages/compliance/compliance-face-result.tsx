// 两张图片对比结果视图：步骤条 + 图片 A（人脸框标注）与对比图片并排 + 小结、逐脸明细和建议。
// 两侧图片一开始就显示；Agent 提交报告后，人脸框、相似度、结论、依据和建议一起出现。
// 数字和结论由服务端按引擎证据与阈值生成，依据、小结、建议是 Agent 的判断；这里只展示。

import { Button } from "antd";
import { Bot, Check, Loader2, Plus } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import type { ReactNode } from "react";

import { StatusBadge } from "@/components/ui/base/badges/status-badge";
import { aceternityMotion } from "@/lib/aceternity-motion";
import { cn } from "@/lib/utils";
import { resourceFileUrl } from "@/services/api/resources";
import type { ComplianceCheck, ComplianceStep, FaceVerdict, ImageStyle, OverallVerdict } from "@/services/api/compliance";
import { useGlobalAgentStore } from "@/stores/use-global-agent-store";

type Tone = "success" | "warning" | "error" | "neutral";

const overallMeta: Record<OverallVerdict, { label: string; tone: Tone; summary: string }> = {
    pass: { label: "高相似风险", tone: "error", summary: "两侧图片中的人脸特征高度相似，建议人工复核。" },
    review: { label: "中等相似风险", tone: "warning", summary: "两侧图片存在明显相似点，建议人工确认后再使用。" },
    risk: { label: "较低相似风险", tone: "success", summary: "当前对比范围内未发现高度相似的人脸特征。" },
    no_face: { label: "无法判断", tone: "neutral", summary: "没有检出可用于对比的人脸，请换一张清晰的正面图重试。" },
};

const faceMeta: Record<FaceVerdict, { label: string; tone: Tone; box: string; bar: string }> = {
    match: { label: "高相似", tone: "error", box: "border-status-error", bar: "bg-status-error" },
    suspect: { label: "疑似相似", tone: "warning", box: "border-status-warning", bar: "bg-status-warning" },
    mismatch: { label: "低相似", tone: "success", box: "border-status-success", bar: "bg-status-success" },
};

const styleLabel: Record<ImageStyle, string> = {
    realistic: "写实照片",
    stylized: "风格化图像",
    unclear: "画风无法确认",
};

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

function Steps({ steps }: { steps: ComplianceStep[] }) {
    return (
        <ol className="m-0 flex list-none flex-wrap items-center gap-x-2 gap-y-2 p-0" aria-label="对比进度">
            {steps.map((step, index) => {
                const done = step.state === "done";
                const current = step.state === "active";
                return (
                    <li key={step.id} className="flex items-center gap-2" aria-current={current ? "step" : undefined}>
                        {index ? <span aria-hidden="true" className={cn("h-px w-6 sm:w-10", done || current ? "bg-foreground/40" : "bg-foreground/15")} /> : null}
                        <span
                            className={cn(
                                "grid size-5 place-items-center rounded-full text-tiny font-semibold",
                                done ? "bg-foreground text-background" : current ? "border border-foreground/60 text-foreground" : "border border-foreground/20 text-foreground/40",
                            )}
                        >
                            {done ? <Check className="size-3" aria-hidden="true" /> : current ? <Loader2 className="size-3 animate-spin" aria-hidden="true" /> : index + 1}
                        </span>
                        <span className={cn("text-caption", done || current ? "font-medium text-foreground" : "text-foreground/45")}>{step.label}</span>
                    </li>
                );
            })}
        </ol>
    );
}

function BulletList({ items, className }: { items: string[]; className?: string }) {
    if (!items.length) return null;
    return (
        <ul className={cn("m-0 list-disc pl-5 text-sm leading-6 text-foreground/75", className)}>
            {items.map((item) => (
                <li key={item}>{item}</li>
            ))}
        </ul>
    );
}

export function ComplianceFaceResult({ check, onNew, actions }: { check: ComplianceCheck; onNew: () => void; actions?: ReactNode }) {
    const result = check.status === "done" ? check.result : undefined;
    const failed = check.status === "failed";
    const overall = result ? overallMeta[result.overall] : null;
    const best = result?.faces.length ? Math.max(...result.faces.map((face) => face.similarity)) : null;
    const { match, suspect } = check.thresholds;
    const title = check.subjectName ? `两张图片对比 · ${check.subjectName}` : "两张图片对比";

    return (
        <div className="mx-auto flex w-full max-w-[1080px] flex-col gap-4 pb-28">
            <header className="flex flex-wrap items-center justify-between gap-3">
                <h1 className="m-0 min-w-0 truncate text-lg font-semibold text-foreground">{title}</h1>
                <div className="flex flex-wrap items-center gap-2">
                    {actions}
                    <Button icon={<Plus className="size-4" />} onClick={onNew}>
                        新的对比
                    </Button>
                </div>
            </header>

            <Steps steps={check.steps} />

            {failed ? (
                <Reveal>
                    <p role="alert" className="m-0 rounded-xl border border-status-error/40 bg-status-error/10 px-4 py-3 text-sm text-foreground">
                        {check.error || "对比失败，请稍后重试。"}
                    </p>
                </Reveal>
            ) : null}

            {overall ? (
                <Reveal>
                    <section className="flex flex-wrap items-center gap-x-6 gap-y-3 rounded-xl border border-border bg-card px-5 py-4" aria-label="对比结论">
                        <StatusBadge variant="filled" tone={overall.tone} label={overall.label} />
                        {best === null ? null : (
                            <div className="flex items-baseline gap-1">
                                <span className="text-4xl font-semibold tabular-nums text-foreground">{best}</span>
                                <span className="text-sm text-foreground/60">% 最高相似度</span>
                            </div>
                        )}
                        <p className="m-0 min-w-[240px] flex-1 text-sm leading-6 text-foreground/70">{overall.summary}</p>
                    </section>
                    {result?.humanReview.required ? (
                        <p role="note" className="m-0 mt-3 rounded-xl border border-status-warning/40 bg-status-warning/10 px-4 py-3 text-sm leading-6 text-foreground">
                            <strong className="font-medium">建议人工复核。</strong>
                            {result.humanReview.reason ? `${result.humanReview.reason}。` : null}
                        </p>
                    ) : null}
                </Reveal>
            ) : null}

            {result ? (
                <Reveal>
                    <Panel title="对比小结">
                        <p className="m-0 text-sm leading-6 text-foreground/80">{result.summary}</p>
                        <p className="mb-0 mt-2 text-caption text-foreground/55">
                            图片类型：{styleLabel[result.style]}
                            {result.style === "realistic" ? "" : "，引擎相似度仅供参考"}
                        </p>
                    </Panel>
                </Reveal>
            ) : null}

            <div className="grid gap-4 md:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
                <Panel title="图片 A（待检测）">
                    <div className="relative overflow-hidden rounded-lg bg-muted">
                        <img src={resourceFileUrl(check.target.resourceId)} alt={check.target.name} className="block max-h-[520px] w-full object-contain" />
                        {result?.faces.map((face) => (
                            <motion.div
                                key={face.index}
                                initial={{ opacity: 0 }}
                                animate={{ opacity: 1 }}
                                className={cn("absolute rounded-sm border-2", faceMeta[face.verdict].box)}
                                style={{ left: `${face.box.x}%`, top: `${face.box.y}%`, width: `${face.box.w}%`, height: `${face.box.h}%` }}
                            >
                                <span className="absolute -top-px left-0 -translate-y-full rounded-t-sm bg-background/90 px-1.5 text-tiny font-medium text-foreground">#{face.index}</span>
                            </motion.div>
                        ))}
                        {check.status === "running" ? (
                            <div className="absolute inset-0 grid place-items-center bg-background/40" role="status">
                                <span className="inline-flex items-center gap-2 rounded-full bg-background/90 px-3 py-1 text-caption text-foreground/70">
                                    <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                                    Agent 正在对比…
                                </span>
                            </div>
                        ) : null}
                    </div>
                </Panel>
                <Panel title={`图片 B（对比图 ${check.bases.length} 张）`}>
                    <div className="grid grid-cols-2 gap-2">
                        {check.bases.map((base, index) => (
                            <figure key={base.resourceId} className="m-0">
                                <img src={resourceFileUrl(base.resourceId)} alt={base.name} className="aspect-square w-full rounded-lg bg-muted object-cover" />
                                <figcaption className="mt-1 truncate text-tiny text-foreground/55">对比图 {index + 1}</figcaption>
                            </figure>
                        ))}
                    </div>
                </Panel>
            </div>

            {result ? (
                <Reveal>
                    <Panel title="逐脸明细">
                        {result.faces.length ? (
                            <ul className="m-0 flex list-none flex-col gap-3 p-0">
                                {result.faces.map((face) => (
                                    <li key={face.index} className="flex flex-col gap-2">
                                        <div className="grid grid-cols-[2.5rem_minmax(0,1fr)_auto] items-center gap-3">
                                            <span className="text-sm font-medium text-foreground">#{face.index}</span>
                                            <div className="min-w-0">
                                                <div className="mb-1 flex justify-between text-caption text-foreground/60">
                                                    <span>最接近对比图 {face.bestBaseIndex + 1}</span>
                                                    <span className="tabular-nums">{face.similarity}%</span>
                                                </div>
                                                <div className="h-1.5 overflow-hidden rounded-full bg-foreground/10" role="progressbar" aria-valuenow={face.similarity} aria-valuemin={0} aria-valuemax={100} aria-label={`人脸 ${face.index} 相似度`}>
                                                    <div className={cn("h-full rounded-full", faceMeta[face.verdict].bar)} style={{ width: `${face.similarity}%` }} />
                                                </div>
                                            </div>
                                            <StatusBadge variant="filled" size="sm" tone={faceMeta[face.verdict].tone} label={faceMeta[face.verdict].label} />
                                        </div>
                                        <div className="pl-[2.5rem]">
                                            <BulletList items={face.basis} />
                                            {face.limitations.length ? <p className="mb-0 mt-2 text-caption text-foreground/55">限制：{face.limitations.join("；")}</p> : null}
                                        </div>
                                    </li>
                                ))}
                            </ul>
                        ) : (
                            <p className="m-0 text-sm text-foreground/60">没有检出人脸。</p>
                        )}
                        <p className="mb-0 mt-4 text-caption leading-5 text-foreground/50">
                            判定规则：相似度 ≥ {match}% 为高相似，{suspect}%–{match}% 为疑似相似，低于 {suspect}% 为低相似。相似度和结论由比对引擎的数值与固定阈值生成，Agent 只负责解读，不参与判定。
                        </p>
                    </Panel>
                </Reveal>
            ) : null}

            {result?.suggestions.length ? (
                <Reveal>
                    <Panel title="处置建议">
                        <BulletList items={result.suggestions} />
                    </Panel>
                </Reveal>
            ) : null}

            {result ? <p className="m-0 text-caption leading-5 text-foreground/50">{result.disclaimer}</p> : null}

            <Panel title="继续追问">
                <div className="flex flex-wrap items-center justify-between gap-3">
                    <p className="m-0 min-w-[240px] flex-1 text-sm leading-6 text-foreground/70">想了解某个判断的依据，或需要换一种处理方式，可以在右侧 Agent 面板里接着问，它会基于这次对比回答。</p>
                    <Button icon={<Bot className="size-4" />} onClick={() => useGlobalAgentStore.getState().openAgent()}>
                        向 AI 追问
                    </Button>
                </div>
            </Panel>
        </div>
    );
}
