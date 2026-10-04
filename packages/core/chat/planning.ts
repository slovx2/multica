import type { CardDecision, ChatCard } from "../api/planning-schema";
export type { CardAnswer, CardDecision, ChatCard, ChatCardQuestion } from "../api/planning-schema";

const DECISION_STATUS: Record<CardDecision["action"], ChatCard["status"]> = {
  approve: "approved", reject: "rejected", answer: "answered", dismiss: "dismissed",
};
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
        ...card, status: DECISION_STATUS[decision.action],
        response: { ...decision, answers: decision.answers?.map((answer) => ({ ...answer, skipped: answer.skipped ?? false })) },
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
