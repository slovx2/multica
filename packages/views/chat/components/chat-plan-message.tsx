"use client";

import { ChevronRight, ClipboardList } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import type { ChatCard } from "@multica/core/chat/planning";
import { useT } from "../../i18n";
import { planHeading, planPreview } from "../lib/plan-timeline";

export function usePlanStatusLabel() {
  const { t } = useT("chat");
  return (status: ChatCard["status"]) => {
    switch (status) {
      case "pending":
        return t(($) => $.planning.pending);
      case "approved":
        return t(($) => $.planning.approved);
      case "rejected":
        return t(($) => $.planning.rejected);
      case "answered":
        return t(($) => $.planning.answered);
      case "dismissed":
        return t(($) => $.planning.dismissed);
      default:
        return t(($) => $.planning.superseded);
    }
  };
}

export function PlanStatusBadge({ status }: { status: ChatCard["status"] }) {
  const label = usePlanStatusLabel();
  return (
    <span
      data-status={status}
      className={cn(
        "shrink-0 rounded-full px-2 py-0.5 text-caption",
        status === "pending"
          ? "bg-brand/10 text-brand"
          : status === "approved"
            ? "bg-success/10 text-success"
            : "bg-muted text-muted-foreground",
      )}
    >
      {label(status)}
    </span>
  );
}

/**
 * A plan rendered as the agent's own message in the conversation. The full
 * plan opens in a dialog; the bubble only carries its heading and a preview.
 */
export function ChatPlanMessage({
  card,
  onOpen,
}: {
  card: ChatCard;
  onOpen: (cardId: string) => void;
}) {
  const { t } = useT("chat");
  const title = planHeading(card.payload.markdown);
  const preview = planPreview(card.payload.markdown);
  return (
    <button
      type="button"
      data-slot="chat-plan-message"
      onClick={() => onOpen(card.id)}
      aria-haspopup="dialog"
      className="group flex w-full max-w-xl flex-col gap-1.5 rounded-lg border border-surface-border bg-surface-raised px-3 py-2.5 text-left transition-colors hover:border-brand/40 hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
    >
      <span className="flex min-w-0 items-center gap-2">
        <ClipboardList className="size-4 shrink-0 text-brand" aria-hidden="true" />
        <span className="shrink-0 text-label font-medium text-foreground">{t(($) => $.planning.plan_label)}</span>
        {title && <span className="min-w-0 truncate text-label text-muted-foreground">{title}</span>}
        <span className="ml-auto flex shrink-0 items-center gap-1">
          <PlanStatusBadge status={card.status} />
          <ChevronRight className="size-4 text-muted-foreground group-hover:text-foreground" aria-hidden="true" />
        </span>
      </span>
      {preview.length > 0 && (
        <span className="line-clamp-3 text-body text-muted-foreground">
          {preview.join(" · ")}
        </span>
      )}
    </button>
  );
}
