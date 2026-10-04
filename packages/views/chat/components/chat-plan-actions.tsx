"use client";

import { useRef, useState } from "react";
import { ClipboardList } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useChatCardDecision, type ChatCard } from "@multica/core/chat/planning";
import { useT } from "../../i18n";
import { planHeading } from "../lib/plan-timeline";
import { CHAT_COLUMN, CHAT_GUTTER } from "./chat-column";

/**
 * Approve / request changes / reject for one pending plan. Shared by the
 * composer bar and the plan dialog footer; "request changes" is a rejection
 * that carries the typed feedback.
 */
export function ChatPlanDecisionButtons({
  sessionId,
  card,
  disabled,
  onView,
  className,
}: {
  sessionId: string;
  card: ChatCard;
  disabled?: boolean;
  /** Present on the composer bar only; the dialog already shows the plan. */
  onView?: () => void;
  className?: string;
}) {
  const { t } = useT("chat");
  const decision = useChatCardDecision(sessionId);
  const submitting = useRef(false);
  const [revising, setRevising] = useState(false);
  const [feedback, setFeedback] = useState("");
  const inactive = !!disabled || card.status !== "pending" || decision.isPending || decision.isSuccess;

  function decide(action: "approve" | "reject", text = "") {
    if (inactive || submitting.current) return;
    submitting.current = true;
    decision.mutate(
      { card_id: card.id, action, feedback: text },
      { onError: () => { submitting.current = false; } },
    );
  }

  return (
    <div className={cn("space-y-2", className)}>
      <div className="flex flex-wrap items-center justify-end gap-2">
        {onView && (
          <Button variant="ghost" size="sm" className="mr-auto" onClick={onView}>
            {t(($) => $.planning.view)}
          </Button>
        )}
        <Button
          variant="outline"
          size="sm"
          disabled={inactive}
          aria-expanded={revising}
          onClick={() => setRevising((value) => !value)}
        >
          {t(($) => $.planning.revise)}
        </Button>
        <Button variant="outline" size="sm" disabled={inactive} onClick={() => decide("reject")}>
          {t(($) => $.planning.reject)}
        </Button>
        <Button
          size="sm"
          disabled={inactive}
          aria-busy={decision.isPending}
          onClick={() => decide("approve")}
        >
          {t(($) => $.planning.approve)}
        </Button>
      </div>
      {revising && (
        <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
          <textarea
            autoFocus
            rows={2}
            className="min-h-16 w-full flex-1 resize-y rounded-md border border-surface-border bg-surface p-2 text-body outline-none focus-visible:border-brand"
            aria-label={t(($) => $.planning.feedback)}
            placeholder={t(($) => $.planning.revise_placeholder)}
            value={feedback}
            disabled={inactive}
            onChange={(event) => setFeedback(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && (event.metaKey || event.ctrlKey) && feedback.trim()) {
                event.preventDefault();
                decide("reject", feedback.trim());
              }
            }}
          />
          <Button
            size="sm"
            disabled={inactive || !feedback.trim()}
            onClick={() => decide("reject", feedback.trim())}
          >
            {t(($) => $.planning.send_revision)}
          </Button>
        </div>
      )}
      {decision.error && (
        <p role="alert" className="text-caption text-destructive">{decision.error.message}</p>
      )}
    </div>
  );
}

/** One-line decision bar above the composer while the latest row is a pending plan. */
export function ChatPlanActions({
  sessionId,
  card,
  disabled,
  onView,
}: {
  sessionId: string;
  card: ChatCard;
  disabled?: boolean;
  onView: () => void;
}) {
  const { t } = useT("chat");
  const title = planHeading(card.payload.markdown) ?? t(($) => $.planning.plan_label);
  return (
    <div className={cn(CHAT_GUTTER, "pb-2")}>
      <section
        data-slot="chat-plan-actions"
        aria-label={t(($) => $.planning.awaiting)}
        className={cn(CHAT_COLUMN, "rounded-lg border border-surface-border bg-surface-raised px-3 py-2")}
      >
        <div className="mb-1.5 flex min-w-0 items-center gap-2 text-label">
          <ClipboardList className="size-4 shrink-0 text-brand" aria-hidden="true" />
          <span className="shrink-0 text-muted-foreground">{t(($) => $.planning.awaiting)}</span>
          <span className="min-w-0 truncate font-medium text-foreground">{title}</span>
        </div>
        <ChatPlanDecisionButtons sessionId={sessionId} card={card} disabled={disabled} onView={onView} />
      </section>
    </div>
  );
}
