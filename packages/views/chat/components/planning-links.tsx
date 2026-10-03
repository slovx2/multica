"use client";

import { usePlanningLinks } from "@multica/core/chat/planning";
import { useWorkspacePaths } from "@multica/core/paths";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";
import { useState } from "react";
import { ListTodo } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTitle,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { IssueChip } from "../../issues/components/issue-chip";

export function ChatPlanningIssues({ sessionId }: { sessionId: string }) {
  const paths = useWorkspacePaths();
  const { t } = useT("chat");
  const [open, setOpen] = useState(false);
  const { data = [] } = usePlanningLinks(undefined, sessionId);
  if (!data.length) return null;
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger render={<Button variant="ghost" size="xs" />}>
        <ListTodo className="size-3.5" />
        {t(($) => $.planning.issues_count, { count: data.length })}
      </PopoverTrigger>
      <PopoverContent
        side="top"
        align="start"
        className="w-96 max-w-[calc(100vw-2rem)]"
      >
        <PopoverTitle className="text-caption">{t(($) => $.planning.issues)}</PopoverTitle>
        <nav aria-label={t(($) => $.planning.issues)} className="max-h-72 space-y-1 overflow-y-auto">
          {data.map((item) => (
            <AppLink
              key={item.id}
              href={paths.issueDetail(item.id)}
              title={item.title}
              className="block min-w-0"
              onClick={() => setOpen(false)}
            >
              <IssueChip
                issueId={item.id}
                fallbackLabel={item.title}
                showAssignee
                className="mx-0 w-full max-w-full py-1.5 hover:bg-accent"
              />
            </AppLink>
          ))}
        </nav>
      </PopoverContent>
    </Popover>
  );
}

export function PlanningLinks({ issueId, sessionId }: { issueId?: string; sessionId?: string | null }) {
  const paths = useWorkspacePaths();
  const { t } = useT("chat");
  const { data = [] } = usePlanningLinks(issueId, sessionId);
  if (!data.length) return null;
  return <nav className="space-y-1 px-3 py-2 text-caption" aria-label={t(($) => $.planning.links)}>
    <p className="font-medium">{issueId ? t(($) => $.planning.links) : t(($) => $.planning.issues)}</p>
    {data.map((item) => <AppLink className="block truncate text-primary underline" key={item.id}
      href={issueId ? `${paths.chat()}?session=${item.id}` : paths.issueDetail(item.id)}>{item.title || item.id}</AppLink>)}
  </nav>;
}
