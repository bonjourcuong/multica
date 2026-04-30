"use client";

import { useEffect, useRef, useState } from "react";
import { Send } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import type { SendGlobalChatMessageResponse } from "@multica/core/api";
import {
  useGlobalChatMessages,
  useSendGlobalChatMessage,
} from "./use-global-chat";

export interface GlobalChatPaneProps {
  /** Called when the user submits a draft, before the POST resolves. */
  onSubmit?: (body: string) => void;
  /** Called with the server response when the POST succeeds. */
  onResolved?: (resp: SendGlobalChatMessageResponse) => void;
  /** Called when the POST itself fails (transport error, 5xx, etc). */
  onErrored?: (err: Error) => void;
}

/**
 * Left column of `/global/chat`. A persistent dialogue with the user's
 * "global" orchestrator agent. Mentioning `@workspace[:agent]` in a message
 * triggers a backend dispatch into that workspace's mirror session, which
 * surfaces in the corresponding tile on the right.
 *
 * The pane forwards mutation lifecycle hooks to its parent so the view can
 * own per-target tile state without the pane needing the workspace list.
 */
export function GlobalChatPane({
  onSubmit,
  onResolved,
  onErrored,
}: GlobalChatPaneProps = {}) {
  const messages = useGlobalChatMessages();
  const send = useSendGlobalChatMessage({ onSubmit, onResolved, onErrored });
  const [draft, setDraft] = useState("");
  const logEndRef = useRef<HTMLDivElement | null>(null);

  // Autoscroll to the latest message — user expectation matches every other
  // chat surface in the app. jsdom (test env) lacks `scrollIntoView`, so we
  // feature-check before calling rather than registering a polyfill.
  useEffect(() => {
    const node = logEndRef.current;
    if (node && typeof node.scrollIntoView === "function") {
      node.scrollIntoView({ behavior: "smooth", block: "end" });
    }
  }, [messages.data?.length]);

  const submit = () => {
    const body = draft.trim();
    if (!body || send.isPending) return;
    send.mutate(body);
    setDraft("");
  };

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden rounded-md border bg-card text-card-foreground">
      <header className="border-b px-3 py-2">
        <h2 className="text-sm font-semibold">Global chat</h2>
        <p className="text-[11px] text-muted-foreground">
          Talk to your orchestrator. Mention <code>@workspace</code> to dispatch.
        </p>
      </header>
      <ol
        data-testid="global-chat-messages"
        className="flex-1 space-y-2 overflow-y-auto p-3 text-sm"
      >
        {messages.isLoading ? (
          <li className="text-xs text-muted-foreground">Loading…</li>
        ) : messages.data && messages.data.length > 0 ? (
          messages.data.map((m) => (
            <li
              key={m.id}
              className={cn(
                "flex",
                m.author_kind === "user" ? "justify-end" : "justify-start",
              )}
            >
              <span
                className={cn(
                  "inline-block max-w-[85%] whitespace-pre-wrap break-words rounded-md px-3 py-1.5 text-sm",
                  m.author_kind === "user"
                    ? "bg-primary text-primary-foreground"
                    : "bg-muted text-foreground",
                )}
              >
                {m.body}
              </span>
            </li>
          ))
        ) : (
          <li className="text-xs text-muted-foreground">
            No messages yet. Type something below to get started.
          </li>
        )}
        <div ref={logEndRef} />
      </ol>
      {send.isError ? (
        <div
          role="alert"
          className="border-t border-destructive/40 bg-destructive/10 px-3 py-1.5 text-xs text-destructive"
        >
          Could not send the message — try again in a moment.
        </div>
      ) : null}
      <div className="border-t p-2">
        <label htmlFor="global-chat-input" className="sr-only">
          Message
        </label>
        <div className="flex items-end gap-2">
          <textarea
            id="global-chat-input"
            data-testid="global-chat-input"
            className="min-h-[44px] flex-1 resize-none rounded-md border bg-background px-2 py-1.5 text-sm text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            rows={2}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                submit();
              }
            }}
            placeholder="Talk to the global agent…"
            disabled={send.isPending}
          />
          <button
            type="button"
            onClick={submit}
            aria-label="Send message"
            disabled={send.isPending || !draft.trim()}
            className="inline-flex size-9 items-center justify-center rounded-md border bg-primary text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Send className="size-4" aria-hidden="true" />
          </button>
        </div>
      </div>
    </div>
  );
}
