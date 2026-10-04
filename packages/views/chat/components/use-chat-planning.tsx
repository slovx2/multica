"use client";

import { useMemo, useState } from "react";
import { useChatCards } from "@multica/core/chat/planning";
import type { ChatMessage } from "@multica/core/types";
import {
  latestPendingQuestion,
  trailingPendingPlan,
  visiblePlanCards,
} from "../lib/plan-timeline";
import { ChatPlanActions } from "./chat-plan-actions";
import { ChatPlanDialog } from "./chat-plan-dialog";
import { ChatQuestionPanel } from "./chat-question-panel";

/**
 * Planning state for one chat session: plan cards woven into the message
 * list, the pending question answered above the composer, and which plan the
 * dialog shows.
 */
export function useChatPlanning(sessionId: string | null, messages: readonly ChatMessage[]) {
  const { data: cards = [] } = useChatCards(sessionId);
  const planCards = useMemo(() => visiblePlanCards(cards), [cards]);
  const question = useMemo(() => latestPendingQuestion(cards), [cards]);
  const actionablePlan = useMemo(
    () => trailingPendingPlan(messages, planCards),
    [messages, planCards],
  );
  // Scoped to the session so switching chats never carries a dialog along.
  const [open, setOpen] = useState<{ sessionId: string; cardId: string } | null>(null);
  const openPlan =
    open && open.sessionId === sessionId
      ? planCards.find((card) => card.id === open.cardId) ?? null
      : null;
  return {
    sessionId,
    planCards,
    question,
    actionablePlan,
    openPlan,
    openPlanDialog: (cardId: string) => {
      if (sessionId) setOpen({ sessionId, cardId });
    },
    closePlanDialog: () => setOpen(null),
  };
}

export type ChatPlanningState = ReturnType<typeof useChatPlanning>;

/** Everything planning renders between the conversation and the composer. */
export function ChatPlanningComposer({
  planning,
  disabled,
}: {
  planning: ChatPlanningState;
  disabled?: boolean;
}) {
  const { sessionId, question, actionablePlan, openPlan } = planning;
  if (!sessionId) return null;
  return (
    <>
      {question ? (
        <ChatQuestionPanel key={question.id} sessionId={sessionId} card={question} disabled={disabled} />
      ) : actionablePlan ? (
        <ChatPlanActions
          key={actionablePlan.id}
          sessionId={sessionId}
          card={actionablePlan}
          disabled={disabled}
          onView={() => planning.openPlanDialog(actionablePlan.id)}
        />
      ) : null}
      <ChatPlanDialog
        sessionId={sessionId}
        card={openPlan}
        actionable={!!openPlan && openPlan.id === actionablePlan?.id}
        disabled={disabled}
        onClose={planning.closePlanDialog}
      />
    </>
  );
}
