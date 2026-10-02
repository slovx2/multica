"use client";

import { useQuery } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";

export function PlanningLinks({ issueId, sessionId }: { issueId?: string; sessionId?: string | null }) {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const { t } = useT("chat");
  const { data = [] } = useQuery({
    queryKey: ["planning-links", wsId, issueId, sessionId],
    queryFn: () => issueId ? api.listIssuePlanningChats(issueId) : api.listChatPlanningIssues(sessionId!),
    enabled: !!issueId || !!sessionId,
    refetchInterval: 5000,
  });
  if (!data.length) return null;
  return <nav className="space-y-1 px-3 py-2 text-caption" aria-label={t(($) => $.planning.links)}>
    <p className="font-medium">{issueId ? t(($) => $.planning.links) : t(($) => $.planning.issues)}</p>
    {data.map((item) => <AppLink className="block truncate text-primary underline" key={item.id}
      href={issueId ? `${paths.chat()}?session=${item.id}` : paths.issueDetail(item.id)}>{item.title || item.id}</AppLink>)}
  </nav>;
}
