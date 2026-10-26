import { invoke, isTauri } from "@tauri-apps/api/core";

export type Config = { sourceMode: "auto" | "official" | "mirror"; officialUrl: string | null; mirrorUrl: string | null; port: number; openBrowser: boolean };
export type Installed = { version: string; commit: string; [key: string]: unknown };
export type Status = { config: Config; installed: Installed | null; running: boolean; pid: number | null; url: string; platform: string; repositoryUrl: string; recoveryRequired?: boolean };
export type UpdateInfo = { available: boolean; canInstall: boolean; reason: string | null; source: string; manifest: { version: string; commit: string; releaseNotes: string; migration: { dataSchema: number; requiresMigration: boolean } } };
export type OperationProgress = { stage: string; percent: number; message: string };
export function progressPercent(progress: OperationProgress | null): number | undefined {
  return progress && Number.isFinite(progress.percent) && progress.percent > 0
    ? Math.min(100, progress.percent) : undefined;
}
// One request at a time; cancellation also discards any in-flight result.
export function pollOperationProgress(
  read: () => Promise<OperationProgress | null>,
  receive: (progress: OperationProgress | null) => void,
  failed: (error: unknown) => void,
  interval = 800,
): () => void {
  let disposed = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const poll = async () => {
    try { const progress = await read(); if (!disposed) receive(progress); }
    catch (error) { if (!disposed) failed(error); }
    if (!disposed) timer = setTimeout(() => { void poll(); }, interval);
  };
  void poll();
  return () => { disposed = true; clearTimeout(timer); };
}
export const desktop = isTauri();
export async function command<T>(name: string, args?: Record<string, unknown>): Promise<T> {
  if (!desktop) throw new Error("当前仅为网页界面。请运行桌面应用，以使用安装、启动和更新功能。");
  return invoke<T>(name, args);
}
export function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
export function validateConfig(config: Config): string | null {
  if (!Number.isInteger(config.port) || config.port < 1024 || config.port > 65535) return "本地端口应为 1024 到 65535 之间的整数。";
  for (const value of [config.mirrorUrl]) {
    if (!value) continue;
    try { const url = new URL(value); if (url.protocol !== "https:" || url.username || url.password || url.search || url.hash || !url.pathname.endsWith("/")) return "下载源应为以 / 结尾、不包含凭据、查询参数或片段的 HTTPS 基础地址。"; }
    catch { return "下载源地址格式不正确。"; }
  }
  if (config.officialUrl && config.officialUrl.replace(/\/$/, "") !== "https://github.com/ddcat-ai/open-ai-canvas") return "当前版本固定使用影策上游公共仓库。";
  if (config.mirrorUrl && !["https://ghproxy.net/", "https://ghfast.top/", "https://gh-proxy.com/"].includes(config.mirrorUrl)) return "请选择支持的公共 GitHub 镜像。";
  return null;
}
