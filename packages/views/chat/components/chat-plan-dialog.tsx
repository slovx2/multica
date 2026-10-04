"use client";

import { ClipboardList } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Markdown } from "@multica/ui/markdown";
import type { ChatCard } from "@multica/core/chat/planning";
import { useT } from "../../i18n";
import { planHeading } from "../lib/plan-timeline";
import { ChatPlanDecisionButtons } from "./chat-plan-actions";
import { PlanStatusBadge } from "./chat-plan-message";

/**
 * Full plan in a large centred dialog (full screen on phones). Closing never
 * decides anything; the decision footer only appears while the plan is still
 * the actionable tail of the conversation.
 */
export function ChatPlanDialog({
  sessionId,
  card,
  actionable,
  disabled,
  onClose,
}: {
  sessionId: string;
  card: ChatCard | null;
  actionable: boolean;
  disabled?: boolean;
  onClose: () => void;
}) {
  const { t } = useT("chat");
  const title = card ? planHeading(card.payload.markdown) : null;
  return (
    <Dialog open={!!card} onOpenChange={(open) => { if (!open) onClose(); }}>
      {card && (
        <DialogContent
          data-slot="chat-plan-dialog"
          className="flex h-[85vh] w-[min(960px,92vw)] max-w-none flex-col gap-0 overflow-hidden p-0 sm:max-w-none max-sm:h-dvh max-sm:max-h-dvh max-sm:w-screen max-sm:rounded-none"
        >
          <div className="flex shrink-0 items-center gap-2 border-b border-surface-border py-3 pr-12 pl-4">
            <ClipboardList className="size-4 shrink-0 text-brand" aria-hidden="true" />
            <DialogTitle className="min-w-0 truncate text-title-sm">
              {title ?? t(($) => $.planning.plan_label)}
            </DialogTitle>
            <PlanStatusBadge status={card.status} />
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3 sm:px-6">
            <Markdown>{card.payload.markdown ?? ""}</Markdown>
          </div>
          {actionable ? (
            <div className="shrink-0 border-t border-surface-border px-4 py-3">
              <ChatPlanDecisionButtons sessionId={sessionId} card={card} disabled={disabled} />
            </div>
          ) : card.status === "pending" ? (
            <DialogDescription className="shrink-0 border-t border-surface-border px-4 py-2 text-caption text-muted-foreground">
              {t(($) => $.planning.read_only)}
            </DialogDescription>
          ) : null}
        </DialogContent>
      )}
    </Dialog>
  );
}
