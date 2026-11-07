// 合规平台空态：和画布入口（canvas-short-drama-entry）同一套版式与样式，
// 只把颜色变量换成工作台 token。提交后由页面创建核验并进入结果态。

import { App } from "antd";
import { ArrowUp, ImagePlus, ShieldCheck, Upload, X } from "lucide-react";
import { useEffect, useRef, useState, type ChangeEvent, type CSSProperties } from "react";

import "@/components/canvas/canvas-short-drama-entry.css";
import "./compliance-entry.css";
import { complianceSkills, resolveComposerSubmit, type ComplianceSkillId } from "./compliance-check";

const MAX_BASE_IMAGES = 1;

const ENTRY_SUGGESTIONS = ["对比两张图片的肖像相似度", "检查两张人像是否存在明显撞脸风险", "分析真人照片与风格化角色的可识别性"];

const entryStyle = {
    "--canvas-entry-text": "var(--foreground)",
    "--canvas-entry-muted": "var(--muted-foreground)",
    "--canvas-entry-accent": "var(--primary)",
    "--canvas-entry-on-accent": "var(--primary-foreground)",
    "--canvas-entry-surface": "var(--card)",
    "--canvas-entry-border": "var(--border)",
    "--canvas-entry-shadow": "color-mix(in srgb, var(--foreground) 10%, transparent)",
} as CSSProperties;

/** 用户在输入框里选好、还没上传的图片；提交时由页面上传成资源。 */
export type PickedImage = { url: string; name: string; file: File };

export type ComplianceSubmitInput = { prompt: string; subjectName: string; target: PickedImage; bases: PickedImage[] };

function ImageSlot({ label, hint, images, multiple, onPick, onRemove }: { label: string; hint?: string; images: PickedImage[]; multiple: boolean; onPick: (files: File[]) => void; onRemove: (index: number) => void }) {
    const inputRef = useRef<HTMLInputElement>(null);
    const full = multiple ? images.length >= MAX_BASE_IMAGES : images.length >= 1;
    const handleChange = (event: ChangeEvent<HTMLInputElement>) => {
        onPick(Array.from(event.target.files ?? []));
        event.target.value = "";
    };
    return (
        <div className="compliance-entry-slot">
            <div className="compliance-entry-slot-label">
                {label}
                <em aria-hidden="true">*</em>
                {hint ? <small>{hint}</small> : null}
            </div>
            <div className="compliance-entry-slot-items">
                {images.map((image, index) => (
                    <div key={image.url} className="compliance-entry-thumb">
                        <img src={image.url} alt={image.name} />
                        <button type="button" aria-label={`移除 ${image.name}`} onClick={() => onRemove(index)}>
                            <X aria-hidden="true" />
                        </button>
                    </div>
                ))}
                {full ? null : (
                    <button type="button" className="compliance-entry-add" aria-label={`上传${label}`} onClick={() => inputRef.current?.click()}>
                        <ImagePlus aria-hidden="true" />
                        上传
                    </button>
                )}
            </div>
            <input ref={inputRef} type="file" accept="image/*" multiple={multiple} hidden onChange={handleChange} />
        </div>
    );
}

export function ComplianceComposer({
    onSubmit,
    onChat,
    submitting = false,
    leaving = false,
}: {
    onSubmit: (input: ComplianceSubmitInput) => void;
    /** 没选能力也没放图片时，纯文字直接发给 Agent 对话。 */ onChat: (prompt: string) => void;
    /** 正在上传图片并创建核验：不接受重复提交。 */ submitting?: boolean;
    /** 提交成功后输入框飞向右下角的退场动画期间。 */ leaving?: boolean;
}) {
    const { message } = App.useApp();
    const importRef = useRef<HTMLInputElement>(null);
    const [skillId, setSkillId] = useState<ComplianceSkillId | null>(null);
    const [prompt, setPrompt] = useState("");
    const [subjectName, setSubjectName] = useState("");
    const [target, setTarget] = useState<PickedImage | null>(null);
    const [bases, setBases] = useState<PickedImage[]>([]);
    const locked = submitting || leaving;
    // 预览用的 object URL 在移除和离开页面时释放。
    const urlsRef = useRef(new Set<string>());
    useEffect(() => {
        const urls = urlsRef.current;
        return () => {
            urls.forEach((url) => URL.revokeObjectURL(url));
            urls.clear();
        };
    }, []);
    const track = (file: File): PickedImage => {
        const url = URL.createObjectURL(file);
        urlsRef.current.add(url);
        return { url, name: file.name, file };
    };
    const release = (url: string) => {
        URL.revokeObjectURL(url);
        urlsRef.current.delete(url);
    };

    const { mode, canSubmit, missing } = resolveComposerSubmit({ skillId, hasTarget: Boolean(target), baseCount: bases.length, prompt });

    const imagesOf = (files: File[]) => {
        const images = files.filter((file) => file.type.startsWith("image/"));
        if (images.length < files.length) message.warning("只支持图片文件");
        return images;
    };
    const pickTarget = (files: File[]) => {
        const [file] = imagesOf(files);
        if (!file) return;
        if (target) release(target.url);
        setTarget(track(file));
    };
    const pickBases = (files: File[]) => {
        const accepted = imagesOf(files).slice(0, MAX_BASE_IMAGES - bases.length);
        if (accepted.length) setBases([...bases, ...accepted.map(track)]);
    };
    // 「导入素材」先补图片 A，再补图片 B；顺序和输入框里两个槽位一致。
    const importFiles = (files: File[]) => {
        const images = imagesOf(files);
        if (!images.length) return;
        setSkillId((current) => current ?? "face-compare");
        const [first, ...rest] = images;
        if (!target) setTarget(track(first));
        const forBases = target ? images : rest;
        const accepted = forBases.slice(0, MAX_BASE_IMAGES - bases.length);
        if (accepted.length) setBases([...bases, ...accepted.map(track)]);
    };
    const removeBase = (index: number) => {
        release(bases[index].url);
        setBases(bases.filter((_, i) => i !== index));
    };
    const removeTarget = () => {
        if (target) release(target.url);
        setTarget(null);
    };

    const submit = () => {
        if (!canSubmit || locked) return;
        if (mode === "chat") {
            onChat(prompt.trim());
            setPrompt("");
            return;
        }
        if (!target) return;
        onSubmit({ prompt: prompt.trim(), subjectName: subjectName.trim(), target, bases });
    };

    return (
        <div className={`canvas-entry-stage compliance-entry-stage${leaving ? " is-leaving" : ""}`} style={entryStyle} aria-busy={locked || undefined}>
            <div className="canvas-entry-ripple" aria-hidden="true">
                <span />
                <span />
                <span />
                <span />
            </div>
            <div className="canvas-entry-shell">
                <span className="canvas-entry-eyebrow">
                    <ShieldCheck aria-hidden="true" />
                    AI 合规工作台
                </span>
                <h2 className="canvas-entry-title">
                    让创作，<strong>合规无忧。</strong>
                </h2>
                <p className="canvas-entry-subtitle">选择两张图片进行肖像对比；网络批量对比即将上线。</p>
                <form
                    className="canvas-entry-composer"
                    onSubmit={(event) => {
                        event.preventDefault();
                        submit();
                    }}
                >
                    <textarea
                        value={prompt}
                        rows={2}
                        placeholder="补充对比目的或使用场景…"
                        aria-label="补充对比目的或使用场景"
                        onChange={(event) => setPrompt(event.target.value)}
                        onKeyDown={(event) => {
                            if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
                                event.preventDefault();
                                submit();
                            }
                        }}
                    />
                    {skillId === "face-compare" ? (
                        <div className="compliance-entry-slots">
                            <ImageSlot label="图片 A（待检测）" images={target ? [target] : []} multiple={false} onPick={pickTarget} onRemove={removeTarget} />
                            <ImageSlot label="图片 B（对比图）" images={bases} multiple={false} onPick={pickBases} onRemove={removeBase} />
                            <label className="compliance-entry-subject">
                                结果名称
                                <input value={subjectName} onChange={(event) => setSubjectName(event.target.value)} placeholder="选填，例如角色 A 对比" />
                            </label>
                        </div>
                    ) : null}
                    <div className="canvas-entry-composer-bar">
                        <button type="button" className="canvas-entry-ghost" onClick={() => importRef.current?.click()}>
                            <Upload aria-hidden="true" />
                            导入素材
                        </button>
                        <span className="canvas-entry-hint">{missing.length ? `还需要：${missing.join("、")}` : "Enter 发送 · Shift+Enter 换行"}</span>
                        <button type="submit" className="canvas-entry-send" disabled={!canSubmit || locked} aria-label={mode === "chat" ? "发送给 Agent" : "开始对比"}>
                            <ArrowUp aria-hidden="true" />
                        </button>
                    </div>
                    <input
                        ref={importRef}
                        type="file"
                        accept="image/*"
                        multiple
                        hidden
                        onChange={(event) => {
                            importFiles(Array.from(event.target.files ?? []));
                            event.target.value = "";
                        }}
                    />
                </form>
                <div className="canvas-entry-suggestions" role="list" aria-label="对比示例">
                    {ENTRY_SUGGESTIONS.map((item) => (
                        <button
                            key={item}
                            type="button"
                            role="listitem"
                            onClick={() => {
                                setPrompt(item);
                                setSkillId((current) => current ?? "face-compare");
                            }}
                        >
                            {item}
                        </button>
                    ))}
                </div>
                <div className="canvas-entry-paths compliance-entry-paths" role="group" aria-label="对比方式">
                    {complianceSkills.map((skill) => {
                        const Icon = skill.icon;
                        return (
                            <button key={skill.id} type="button" disabled={!skill.ready} aria-pressed={skillId === skill.id} onClick={() => setSkillId(skillId === skill.id ? null : skill.id)}>
                                <Icon aria-hidden="true" />
                                <span>
                                    <strong>{skill.title}</strong>
                                    <small>{skill.hint}</small>
                                </span>
                            </button>
                        );
                    })}
                </div>
            </div>
        </div>
    );
}
