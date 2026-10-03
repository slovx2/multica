"use client";

import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { chatKeys } from "@multica/core/chat/queries";
import { projectResourcesOptions } from "@multica/core/projects";
import { runtimeListOptions } from "@multica/core/runtimes";
import type { ChatContextState } from "@multica/core/types/chat";
import type { LocalDirectoryResourceRef } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
} from "@multica/ui/components/ui/alert-dialog";
import { useT } from "../../i18n";

export function ChatContextBadge({
  state,
  supported = true,
  running = true,
}: {
  state?: ChatContextState;
  supported?: boolean;
  running?: boolean;
}) {
  const { t } = useT("chat");
  if (!supported) return null;
  const usage = state?.usage;
  const compact =
    state?.compaction?.status === "started" && !running
      ? null
      : state?.compaction;
  const percent = usage ? Math.round((100 * usage.used) / usage.window) : 0;
  return (
    <>
      {usage && (
        <span
          className={`shrink-0 text-caption tabular-nums ${usage.used / usage.window > 0.95 ? "text-destructive" : usage.used / usage.window > 0.8 ? "text-warning" : "text-muted-foreground"}`}
          title={t(($) => $.context.tokens, {
            used: usage.used.toLocaleString(),
            window: usage.window.toLocaleString(),
          })}
        >
          {t(($) => $.context.usage_label, {
            percent,
            window: Math.round(usage.window / 1000),
          })}
        </span>
      )}
      {compact && (
        <span
          role={compact.status === "failed" ? "alert" : "status"}
          title={compact.error}
          className={`text-caption ${compact.status === "failed" ? "text-destructive" : "text-muted-foreground"}`}
        >
          {compact.status === "started"
            ? t(($) => $.context.compacting)
            : compact.status === "failed"
              ? t(($) => $.context.compaction_failed)
              : compact.pre_tokens != null && compact.post_tokens != null
                ? t(($) => $.context.compacted_counts, {
                    before: (compact.pre_tokens / 1000).toFixed(1),
                    after: (compact.post_tokens / 1000).toFixed(1),
                  })
                : t(($) => $.context.compacted)}
        </span>
      )}
    </>
  );
}

export function CompactContextDialog({
  sessionId,
  open,
  onOpenChange,
}: {
  sessionId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("chat");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const compact = useMutation({
    mutationFn: () => api.compactChatContext(sessionId),
    onSuccess: () => {
      onOpenChange(false);
      void qc.invalidateQueries({ queryKey: chatKeys.all(wsId) });
      void qc.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) });
    },
    onError: (error: Error) => toast.error(error.message),
  });
  return (
    <AlertDialog
      open={open}
      onOpenChange={(value) => {
        if (!compact.isPending) onOpenChange(value);
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t(($) => $.context.compact)}</AlertDialogTitle>
          <AlertDialogDescription>
            {t(($) => $.context.warning)}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <Button
            variant="outline"
            disabled={compact.isPending}
            onClick={() => onOpenChange(false)}
          >
            {t(($) => $.context.cancel)}
          </Button>
          <Button
            disabled={compact.isPending}
            aria-busy={compact.isPending}
            onClick={() => compact.mutate()}
          >
            {t(($) => $.context.compact)}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

export function ChatDirectorySync({
  sessionId,
  projectId,
  runtimeId,
  disabled,
}: {
  sessionId: string;
  projectId: string;
  runtimeId?: string;
  disabled?: boolean;
}) {
  const { t } = useT("chat");
  const wsId = useWorkspaceId();
  const resources = useQuery(projectResourcesOptions(wsId, projectId));
  const runtimes = useQuery(runtimeListOptions(wsId));
  const runtime = runtimes.data?.find((value) => value.id === runtimeId);
  const available = resources.data?.some(
    (value) =>
      value.resource_type === "local_directory" &&
      (value.resource_ref as LocalDirectoryResourceRef).daemon_id ===
        runtime?.daemon_id,
  );
  const [request, setRequest] = useState<{
    session: string;
    id: string;
  } | null>(null);
  const sync = useMutation({
    mutationFn: () => api.syncChatDirectory(sessionId),
    onSuccess: (value) => setRequest({ session: sessionId, id: value.id }),
    onError: (error: Error) => toast.error(error.message),
  });
  const status = useQuery({
    queryKey: ["chat-directory-sync", wsId, request?.session, request?.id],
    queryFn: () => api.getChatDirectorySync(request!.session, request!.id),
    enabled: !!request,
    refetchInterval: (query) =>
      query.state.data?.status === "completed" ||
      query.state.data?.status === "timeout"
        ? false
        : 1000,
    retry: false,
  });
  useEffect(() => {
    if (!request) return;
    if (status.error) {
      toast.error(status.error.message);
      setRequest(null);
      return;
    }
    if (status.data?.status === "timeout") {
      toast.error(t(($) => $.context.timeout));
      setRequest(null);
      return;
    }
    if (status.data?.status !== "completed") return;
    const result = status.data.result;
    if (result?.status === "current")
      toast.success(t(($) => $.context.current));
    else if (result?.status === "updated")
      toast.success(t(($) => $.context.updated, { count: result.updated }));
    else {
      const reason = result?.reason;
      const known = [
        "dirty",
        "ahead",
        "diverged",
        "no_upstream",
        "detached_head",
        "fetch_failed",
        "fetch_only",
        "directory_busy",
        "fast_forward_failed",
        "comparison_failed",
        "status_failed",
        "disabled",
        "unsupported_mode",
        "invalid_directory",
      ] as const;
      const key = known.find((value) => value === reason);
      const label = key ? t(($) => $.context[key]) : (reason ?? "");
      toast.info(
        t(($) => $.context.skipped, { reason: label }) +
          (result?.behind ? ` (${result.upstream}: -${result.behind})` : ""),
      );
    }
    setRequest(null);
  }, [request, status.data, status.error, t]);
  if (!available) return null;
  const pending = sync.isPending || !!request;
  return (
    <Button
      type="button"
      variant="ghost"
      size="xs"
      disabled={disabled || pending}
      aria-busy={pending}
      title={t(($) => $.context.sync_hint)}
      onClick={() => sync.mutate()}
    >
      {pending ? t(($) => $.context.syncing) : t(($) => $.context.sync)}
    </Button>
  );
}
