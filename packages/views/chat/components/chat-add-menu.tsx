"use client";

import { useRef, type ReactNode } from "react";
import { Check, FolderKanban, Image as ImageIcon, Plus, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { cn } from "@multica/ui/lib/utils";
import type { Project } from "@multica/core/types";
import { ProjectIcon } from "../../projects/components/project-icon";
import { useT } from "../../i18n";

/**
 * Touch-friendly sizing for the composer "+" menu and the items it hosts
 * (ChatSettingsMenu). Scoped here rather than in the shared dropdown
 * primitive: coarse pointers get 44px rows, desktop stays compact.
 */
export const CHAT_MENU_ITEM_CLASS =
  "min-h-8 gap-2 px-2 pointer-coarse:min-h-11 pointer-coarse:gap-3 pointer-coarse:px-3 pointer-coarse:text-body-lg";
export const CHAT_MENU_CONTENT_CLASS = "min-w-56";
export const CHAT_SUBMENU_CONTENT_CLASS =
  "min-w-48 max-w-[calc(100vw-1rem)] pointer-coarse:min-w-52";

interface ChatAddMenuProps {
  extraItems?: ReactNode;
  /** Called with each selected file — the caller routes it through the
   *  editor's upload extension, same path as paste / drag-drop. */
  onSelectFile?: (file: File) => void;
  projects?: Project[];
  projectId?: string | null;
  onSelectProject?: (projectId: string | null) => void;
  /** Soft warning: the active agent's daemon is too old to receive the
   *  project description. Selection stays enabled; the submenu only appends
   *  an explanatory hint so the user knows before choosing. */
  projectContextUnsupported?: boolean;
  disabled?: boolean;
}

/**
 * The "+" affordance at the bottom-left of the chat composer. Replaces the
 * standalone paperclip button: file upload now lives here as a submenu entry,
 * leaving room for future add-actions (agents, skills, tools) under one entry
 * point without crowding the input bar.
 */
export function ChatAddMenu({
  extraItems,
  onSelectFile,
  projects = [],
  projectId,
  onSelectProject,
  projectContextUnsupported,
  disabled,
}: ChatAddMenuProps) {
  const { t } = useT("chat");
  const inputRef = useRef<HTMLInputElement>(null);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files ?? []);
    if (files.length === 0) return;
    e.target.value = "";
    for (const file of files) onSelectFile?.(file);
  };

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              disabled={disabled}
              aria-label={t(($) => $.input.add_tooltip)}
              title={t(($) => $.input.add_tooltip)}
              className="rounded-full text-muted-foreground"
            >
              <Plus />
            </Button>
          }
        />
        <DropdownMenuContent
          align="start"
          side="top"
          sideOffset={6}
          className={CHAT_MENU_CONTENT_CLASS}
        >
          {extraItems}
          {onSelectFile && (
            <DropdownMenuItem
              className={CHAT_MENU_ITEM_CLASS}
              onClick={() => inputRef.current?.click()}
            >
              <ImageIcon />
              {t(($) => $.input.upload_file)}
            </DropdownMenuItem>
          )}
          {onSelectProject && (
            <DropdownMenuSub>
              <DropdownMenuSubTrigger className={CHAT_MENU_ITEM_CLASS}>
                <FolderKanban />
                {t(($) => $.input.project_context)}
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent
                className={cn(CHAT_SUBMENU_CONTENT_CLASS, "max-h-72 overflow-y-auto")}
              >
                {projects.map((project) => (
                  <DropdownMenuItem
                    key={project.id}
                    className={CHAT_MENU_ITEM_CLASS}
                    onClick={() => onSelectProject(project.id)}
                  >
                    <ProjectIcon project={project} size="md" />
                    <span className="min-w-0 flex-1 truncate">{project.title}</span>
                    {project.id === projectId && <Check className="ml-auto" />}
                  </DropdownMenuItem>
                ))}
                {projects.length === 0 && (
                  <div className="px-2 py-1.5 text-caption text-muted-foreground">
                    {t(($) => $.input.no_projects)}
                  </div>
                )}
                {projectId && <DropdownMenuSeparator />}
                {projectId && (
                  <DropdownMenuItem
                    className={CHAT_MENU_ITEM_CLASS}
                    onClick={() => onSelectProject(null)}
                  >
                    <X />
                    {t(($) => $.input.remove_project_context)}
                  </DropdownMenuItem>
                )}
                {projectContextUnsupported && (
                  <>
                    <DropdownMenuSeparator />
                    <div className="max-w-56 px-2 py-1.5 text-caption text-muted-foreground">
                      {t(($) => $.input.project_context_unsupported)}
                    </div>
                  </>
                )}
              </DropdownMenuSubContent>
            </DropdownMenuSub>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      {onSelectFile && (
        <input
          ref={inputRef}
          type="file"
          multiple
          className="hidden"
          onChange={handleChange}
        />
      )}
    </>
  );
}
