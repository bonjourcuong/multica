"use client";

import { useMemo } from "react";
import { Bot, ChevronDown, Check } from "lucide-react";
import {
  Avatar,
  AvatarFallback,
  AvatarImage,
} from "@multica/ui/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import type { Agent } from "@multica/core/types";

export interface AgentDropdownProps {
  /** Agents to render in the dropdown. Caller is responsible for filtering archived rows when undesired. */
  agents: Agent[];
  /** Currently active agent — drives the trigger label and check mark. */
  activeAgent: Agent | null;
  /** Optional viewer id; when provided, agents owned by the viewer are grouped under "My agents". */
  userId: string | undefined;
  /** Fired when the user picks an agent from the menu. */
  onSelect: (agent: Agent) => void;
}

export function AgentDropdown({
  agents,
  activeAgent,
  userId,
  onSelect,
}: AgentDropdownProps) {
  const { mine, others } = useMemo(() => {
    const mine: Agent[] = [];
    const others: Agent[] = [];
    for (const a of agents) {
      if (userId && a.owner_id === userId) mine.push(a);
      else others.push(a);
    }
    return { mine, others };
  }, [agents, userId]);

  if (!activeAgent) {
    return <span className="text-xs text-muted-foreground">No agents</span>;
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        data-testid="agent-dropdown-trigger"
        className="flex items-center gap-1.5 rounded-md px-1.5 py-1 -ml-1 cursor-pointer outline-none transition-colors hover:bg-accent aria-expanded:bg-accent"
      >
        <AgentAvatarSmall agent={activeAgent} />
        <span className="text-xs font-medium max-w-28 truncate">
          {activeAgent.name}
        </span>
        <ChevronDown className="size-3 text-muted-foreground shrink-0" />
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="start"
        side="top"
        className="max-h-80 w-auto max-w-64"
      >
        {mine.length > 0 && (
          <DropdownMenuGroup>
            <DropdownMenuLabel>My agents</DropdownMenuLabel>
            {mine.map((agent) => (
              <AgentMenuItem
                key={agent.id}
                agent={agent}
                isCurrent={agent.id === activeAgent.id}
                onSelect={onSelect}
              />
            ))}
          </DropdownMenuGroup>
        )}
        {mine.length > 0 && others.length > 0 && <DropdownMenuSeparator />}
        {others.length > 0 && (
          <DropdownMenuGroup>
            {mine.length > 0 && <DropdownMenuLabel>Others</DropdownMenuLabel>}
            {others.map((agent) => (
              <AgentMenuItem
                key={agent.id}
                agent={agent}
                isCurrent={agent.id === activeAgent.id}
                onSelect={onSelect}
              />
            ))}
          </DropdownMenuGroup>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function AgentMenuItem({
  agent,
  isCurrent,
  onSelect,
}: {
  agent: Agent;
  isCurrent: boolean;
  onSelect: (agent: Agent) => void;
}) {
  return (
    <DropdownMenuItem
      onClick={() => onSelect(agent)}
      className="flex min-w-0 items-center gap-2"
    >
      <AgentAvatarSmall agent={agent} />
      <span className="truncate flex-1">{agent.name}</span>
      {isCurrent && (
        <Check className="size-3.5 text-muted-foreground shrink-0" />
      )}
    </DropdownMenuItem>
  );
}

export function AgentAvatarSmall({ agent }: { agent: Agent | null }) {
  return (
    <Avatar className="size-6 shrink-0">
      {agent?.avatar_url && <AvatarImage src={agent.avatar_url} />}
      <AvatarFallback className="bg-purple-100 text-purple-700">
        <Bot className="size-3.5" />
      </AvatarFallback>
    </Avatar>
  );
}
