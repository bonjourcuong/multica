/**
 * Public surface for the cross-workspace orchestrator chat view.
 * The route shell at `apps/web/app/global/chat/page.tsx` consumes
 * `GlobalChatView` directly; everything else (panes, tiles, hooks)
 * is internal to this package and intentionally not re-exported.
 */
export { GlobalChatView } from "./global-chat-view";
