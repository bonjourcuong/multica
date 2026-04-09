"use client";

import { useState } from "react";
import { Plus, FolderKanban } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { projectListOptions } from "@multica/core/projects/queries";
import { useCreateProject } from "@multica/core/projects/mutations";
import { PROJECT_STATUS_CONFIG } from "@multica/core/projects/config";
import { useWorkspaceId } from "@multica/core/hooks";
import { AppLink } from "../../navigation";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import type { Project } from "@multica/core/types";

function ProjectCard({ project }: { project: Project }) {
  const statusCfg = PROJECT_STATUS_CONFIG[project.status];
  return (
    <AppLink
      href={`/projects/${project.id}`}
      className="flex items-center gap-3 rounded-lg border px-4 py-3 hover:bg-accent/50 transition-colors"
    >
      <span className="text-lg shrink-0">{project.icon || "📁"}</span>
      <div className="min-w-0 flex-1">
        <div className="font-medium truncate">{project.title}</div>
        {project.description && (
          <div className="text-xs text-muted-foreground truncate mt-0.5">
            {project.description}
          </div>
        )}
      </div>
      <span className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium ${statusCfg.badgeBg} ${statusCfg.badgeText}`}>
        {statusCfg.label}
      </span>
    </AppLink>
  );
}

function CreateProjectDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const [title, setTitle] = useState("");
  const createProject = useCreateProject();

  const handleCreate = () => {
    if (!title.trim()) return;
    createProject.mutate({ title: title.trim() }, {
      onSuccess: () => { setTitle(""); onOpenChange(false); },
    });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create project</DialogTitle>
        </DialogHeader>
        <Input
          placeholder="Project title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") handleCreate(); }}
          autoFocus
        />
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button onClick={handleCreate} disabled={!title.trim() || createProject.isPending}>Create</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function ProjectsPage() {
  const wsId = useWorkspaceId();
  const { data: projects = [], isLoading } = useQuery(projectListOptions(wsId));
  const [createOpen, setCreateOpen] = useState(false);

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b px-6 py-3">
        <div className="flex items-center gap-2">
          <FolderKanban className="h-4 w-4 text-muted-foreground" />
          <h1 className="text-sm font-medium">Projects</h1>
        </div>
        <Button size="sm" variant="outline" onClick={() => setCreateOpen(true)}>
          <Plus className="h-3.5 w-3.5 mr-1" />
          New project
        </Button>
      </div>

      <div className="flex-1 overflow-y-auto p-6">
        {isLoading ? (
          <div className="space-y-3">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="h-16 w-full rounded-lg" />
            ))}
          </div>
        ) : projects.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20 text-muted-foreground">
            <FolderKanban className="h-10 w-10 mb-3 opacity-40" />
            <p className="text-sm">No projects yet</p>
            <Button size="sm" variant="outline" className="mt-3" onClick={() => setCreateOpen(true)}>
              Create your first project
            </Button>
          </div>
        ) : (
          <div className="space-y-2">
            {projects.map((project) => (
              <ProjectCard key={project.id} project={project} />
            ))}
          </div>
        )}
      </div>

      <CreateProjectDialog open={createOpen} onOpenChange={setCreateOpen} />
    </div>
  );
}
