"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@multica/ui/components/ui/button";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useChatStore } from "@multica/core/chat";
import { chatKeys } from "@multica/core/chat/queries";
import { runtimeListOptions } from "@multica/core/runtimes";
import { toast } from "sonner";
import { useT } from "../../i18n";

export function ChatPlanMode({ sessionId, runtimeId, disabled }: { sessionId?: string | null; runtimeId?: string; disabled?: boolean }) {
  const { t } = useT("chat");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const runtimes = useQuery(runtimeListOptions(wsId));
  const provider = runtimes.data?.find((r) => r.id === runtimeId)?.provider;
  const draftMode = useChatStore((s) => s.draftPlanMode);
  const setDraftMode = useChatStore((s) => s.setDraftPlanMode);
  const session = useQuery({
    queryKey: chatKeys.session(wsId, sessionId ?? ""),
    queryFn: () => api.getChatSession(sessionId!),
    enabled: !!sessionId,
  });
  const enabled = sessionId ? session.data?.plan_mode === true : draftMode;
  const update = useMutation({
    mutationFn: (planMode: boolean) => api.updateChatSession(sessionId!, { plan_mode: planMode }),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: chatKeys.all(wsId) }); },
    onError: (err: Error) => toast.error(err.message),
  });
  return <Button size="xs" variant={enabled ? "brand" : "outline"} aria-pressed={enabled}
    disabled={disabled || update.isPending || (!!sessionId && !session.data)}
    onClick={() => { if (!enabled && provider && provider !== "claude" && provider !== "codex") { toast.error(t(($) => $.planning.unsupported)); return; } if (sessionId) update.mutate(!enabled); else setDraftMode(!enabled); }}>
    {t(($) => $.planning.mode)}
  </Button>;
}
