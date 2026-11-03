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
 * 与面板宽度一致：只有页面发布了上下文（画布或合规核验）、面板打开、用户选择停靠且视口足够宽时才生效。
 */
export function useAgentPanelDock() {
    const open = useGlobalAgentStore((state) => state.open);
    const hasContext = useGlobalAgentStore((state) => state.canvas !== null || state.page !== null);
    const docked = useGlobalAgentStore((state) => state.dock.docked);
    const storedWidth = useGlobalAgentStore((state) => state.dock.width);
    const width = useSyncExternalStore(subscribeViewport, viewportWidth, serverViewportWidth);
    const available = hasContext && width >= AGENT_DOCK_MIN_VIEWPORT;
    return {
        /** 当前页面可以停靠（页面发布了上下文且视口够宽）。 */
        available,
        /** 正在停靠：面板贴右侧全高，工作区让出 width。 */
        active: open && docked && available,
        width: clampAgentDockWidth(storedWidth, width),
        bounds: agentDockWidthBounds(width),
    };
}
