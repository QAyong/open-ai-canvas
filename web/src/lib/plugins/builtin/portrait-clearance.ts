import { registerPlugin } from "@/lib/plugins/plugin-registry";
import type { PluginManifest, RegisteredPlugin } from "@/lib/plugins/plugin-types";

import { PORTRAIT_CLEARANCE_PLUGIN_ID } from "@/lib/portrait-clearance/contracts";

const manifest: PluginManifest = {
    id: PORTRAIT_CLEARANCE_PLUGIN_ID,
    name: "肖像权可识别性排查",
    version: "0.1.0",
    apiVersion: "1",
    category: "canvas-node",
    description: "对虚拟人和人物图片执行本地人脸预检、网络候选排查与审慎风险报告。",
    author: "影策团队",
    surfaces: ["node", "fullscreen"],
    permissions: [
        "canvas.read",
        "canvas.write",
        "asset.read",
        "asset.import",
        "ai.text",
        "external.network",
        "external.upload",
        "external.open",
    ],
    trusted: true,
};

export const portraitClearancePlugin: RegisteredPlugin = { manifest };

registerPlugin(portraitClearancePlugin);
