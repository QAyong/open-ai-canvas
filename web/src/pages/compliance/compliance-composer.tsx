// 合规平台空态：和画布入口（canvas-short-drama-entry）同一套版式与样式，
// 只把颜色变量换成工作台 token。提交后由页面创建核验并进入结果态。

import { App } from "antd";
import { ArrowUp, ImagePlus, ShieldCheck, Upload, X } from "lucide-react";
import { useRef, useState, type ChangeEvent, type CSSProperties } from "react";

import "@/components/canvas/canvas-short-drama-entry.css";
import "./compliance-entry.css";
import { complianceSkills, type ComplianceImage, type ComplianceSkillId, type NewComplianceCheck } from "./compliance-check";

const MAX_BASE_IMAGES = 3;

const ENTRY_SUGGESTIONS = ["核验这张剧照里的人是否为已授权演员", "检查海报人像与授权底图是否一致", "对比两张图是不是同一个人"];

const entryStyle = {
    "--canvas-entry-text": "var(--foreground)",
    "--canvas-entry-muted": "var(--muted-foreground)",
    "--canvas-entry-accent": "var(--primary)",
    "--canvas-entry-on-accent": "var(--primary-foreground)",
    "--canvas-entry-surface": "var(--card)",
    "--canvas-entry-border": "var(--border)",
    "--canvas-entry-shadow": "color-mix(in srgb, var(--foreground) 10%, transparent)",
} as CSSProperties;

function toImage(file: File): ComplianceImage {
    return { url: URL.createObjectURL(file), name: file.name };
}

function ImageSlot({ label, hint, images, multiple, onPick, onRemove }: { label: string; hint?: string; images: ComplianceImage[]; multiple: boolean; onPick: (files: File[]) => void; onRemove: (index: number) => void }) {
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

export function ComplianceComposer({ onSubmit, leaving = false }: { onSubmit: (input: NewComplianceCheck) => void; /** 提交后输入框飞向右下角的退场动画期间，不再接受输入和重复提交。 */ leaving?: boolean }) {
    const { message } = App.useApp();
    const importRef = useRef<HTMLInputElement>(null);
    const [skillId, setSkillId] = useState<ComplianceSkillId | null>(null);
    const [prompt, setPrompt] = useState("");
    const [subjectName, setSubjectName] = useState("");
    const [target, setTarget] = useState<ComplianceImage | null>(null);
    const [bases, setBases] = useState<ComplianceImage[]>([]);

    const missing = skillId === "face-compare" ? [!target && "待核验素材", !bases.length && "授权底图"].filter(Boolean) : [];
    const canSubmit = skillId === "face-compare" && !missing.length;

    const imagesOf = (files: File[]) => {
        const images = files.filter((file) => file.type.startsWith("image/"));
        if (images.length < files.length) message.warning("只支持图片文件");
        return images;
    };
    const pickTarget = (files: File[]) => {
        const [file] = imagesOf(files);
        if (!file) return;
        if (target) URL.revokeObjectURL(target.url);
        setTarget(toImage(file));
    };
    const pickBases = (files: File[]) => {
        const accepted = imagesOf(files).slice(0, MAX_BASE_IMAGES - bases.length);
        if (accepted.length) setBases([...bases, ...accepted.map(toImage)]);
    };
    // 「导入素材」先补待核验素材，再补授权底图；顺序和输入框里两个槽位一致。
    const importFiles = (files: File[]) => {
        const images = imagesOf(files);
        if (!images.length) return;
        setSkillId((current) => current ?? "face-compare");
        const [first, ...rest] = images;
        if (!target) setTarget(toImage(first));
        const forBases = target ? images : rest;
        const accepted = forBases.slice(0, MAX_BASE_IMAGES - bases.length);
        if (accepted.length) setBases([...bases, ...accepted.map(toImage)]);
    };
    const removeBase = (index: number) => {
        URL.revokeObjectURL(bases[index].url);
        setBases(bases.filter((_, i) => i !== index));
    };
    const removeTarget = () => {
        if (target) URL.revokeObjectURL(target.url);
        setTarget(null);
    };

    const submit = () => {
        if (!canSubmit || !target || leaving) return;
        onSubmit({ skillId: "face-compare", prompt: prompt.trim(), subjectName: subjectName.trim(), target, bases });
    };

    return (
        <div className={`canvas-entry-stage compliance-entry-stage${leaving ? " is-leaving" : ""}`} style={entryStyle} aria-busy={leaving || undefined}>
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
                <p className="canvas-entry-subtitle">说明要核验什么，Agent 帮你调用合规能力，给出结论和处置建议。</p>
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
                        placeholder="描述你要核验的内容…"
                        aria-label="描述你要核验的内容"
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
                            <ImageSlot label="待核验素材" images={target ? [target] : []} multiple={false} onPick={pickTarget} onRemove={removeTarget} />
                            <ImageSlot label="授权底图" hint={`最多 ${MAX_BASE_IMAGES} 张`} images={bases} multiple onPick={pickBases} onRemove={removeBase} />
                            <label className="compliance-entry-subject">
                                授权人
                                <input value={subjectName} onChange={(event) => setSubjectName(event.target.value)} placeholder="选填，用于报告署名" />
                            </label>
                        </div>
                    ) : null}
                    <div className="canvas-entry-composer-bar">
                        <button type="button" className="canvas-entry-ghost" onClick={() => importRef.current?.click()}>
                            <Upload aria-hidden="true" />
                            导入素材
                        </button>
                        <span className="canvas-entry-hint">{missing.length ? `还需要：${missing.join("、")}` : "Enter 发送 · Shift+Enter 换行"}</span>
                        <button type="submit" className="canvas-entry-send" disabled={!canSubmit} aria-label="开始核验">
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
                <div className="canvas-entry-suggestions" role="list" aria-label="核验示例">
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
                <div className="canvas-entry-paths compliance-entry-paths" role="group" aria-label="合规能力">
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
