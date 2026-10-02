import type { CardDecision, ChatCard } from "../api/planning-schema";
export type { CardDecision, ChatCard } from "../api/planning-schema";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import { chatKeys } from "./queries";

export function useChatCards(sessionId: string | null) {
  const wsId = useWorkspaceId();
  return useQuery({
    queryKey: chatKeys.cards(wsId, sessionId),
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
    onSuccess: (_result, decision) => {
      qc.setQueryData<ChatCard[]>(chatKeys.cards(wsId, sessionId), (cards) => cards?.map((card) => card.id === decision.card_id ? {
        ...card, status: decision.action === "approve" ? "approved" : decision.action === "reject" ? "rejected" : "answered", response: decision,
      } : card));
      void qc.invalidateQueries({ queryKey: chatKeys.all(wsId) });
      void qc.invalidateQueries({ queryKey: chatKeys.messages(sessionId) });
      void qc.invalidateQueries({ queryKey: chatKeys.messagesPage(sessionId) });
      void qc.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) });
    },
  });
}

export function usePlanningLinks(issueId?: string, sessionId?: string | null) {
  const wsId = useWorkspaceId();
  return useQuery({
    queryKey: chatKeys.planningLinks(wsId, issueId, sessionId),
    queryFn: () => issueId ? api.listIssuePlanningChats(issueId) : api.listChatPlanningIssues(sessionId!),
    enabled: !!issueId || !!sessionId,
    refetchInterval: 5000,
  });
}
