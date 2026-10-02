import type { CardDecision } from "../api/planning-schema";
export type { CardDecision, ChatCard } from "../api/planning-schema";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import { chatKeys } from "./queries";

export function useChatCards(sessionId: string | null) {
  const wsId = useWorkspaceId();
  return useQuery({
    queryKey: [...chatKeys.all(wsId), "cards", sessionId],
    queryFn: () => api.listChatCards(sessionId!),
    enabled: !!sessionId,
    refetchInterval: 2000,
  });
}
export function useChatCardDecision(sessionId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (decision: CardDecision) => api.sendChatMessage(sessionId, "", undefined, decision),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: chatKeys.all(wsId) });
      void qc.invalidateQueries({ queryKey: chatKeys.messages(sessionId) });
      void qc.invalidateQueries({ queryKey: chatKeys.messagesPage(sessionId) });
      void qc.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) });
    },
  });
}
