import { useSyncExternalStore } from "react";

import { AGENT_DOCK_MIN_VIEWPORT, agentDockWidthBounds, clampAgentDockWidth } from "@/lib/canvas/agent-panel-dock";
import { useGlobalAgentStore } from "@/stores/use-global-agent-store";

function subscribeViewport(onChange: () => void) {
    window.addEventListener("resize", onChange);
    return () => window.removeEventListener("resize", onChange);
}

const viewportWidth = () => window.innerWidth;
const serverViewportWidth = () => 0;

/**
 * Agent 面板是否停靠在页面右侧。工作区布局和面板共用这一份判断，保证页面让出的宽度
 * 与面板宽度一致：画布遵循用户停靠偏好；合规页内嵌右栏不受浮窗偏好影响，窄屏由页面切换两栏。
 */
export function useAgentPanelDock() {
    const open = useGlobalAgentStore((state) => state.open);
    const hasContext = useGlobalAgentStore((state) => state.canvas !== null || Boolean(state.page?.panelHost));
    const embedded = useGlobalAgentStore((state) => Boolean(state.page?.panelHost));
    const docked = useGlobalAgentStore((state) => state.dock.docked);
    const storedWidth = useGlobalAgentStore((state) => state.dock.width);
    const width = useSyncExternalStore(subscribeViewport, viewportWidth, serverViewportWidth);
    const available = hasContext && width >= AGENT_DOCK_MIN_VIEWPORT;
    return {
        /** 当前页面可以停靠（页面发布了上下文且视口够宽）。 */
        available,
        /** 桌面并排：浮动面板由外壳让宽；内嵌面板由页面网格分配 width。 */
        active: open && (embedded || docked) && available,
        /** 合规页已经在页面内分栏，外层布局不能再次让出宽度。 */
        embedded,
        width: clampAgentDockWidth(storedWidth, width),
        bounds: agentDockWidthBounds(width),
    };
}
