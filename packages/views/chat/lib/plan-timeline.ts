import type { ChatCard } from "@multica/core/chat/planning";
import type { ChatMessage } from "@multica/core/types";

export type PlanTimelineEntry<T> =
  | { kind: "item"; item: T }
  | { kind: "plan"; card: ChatCard };

interface TimelineAnchor {
  /** Task whose assistant output this row is; null for user rows. */
  anchorTaskId: string | null;
  createdAt: string;
}

function timeOf(value: string): number {
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? Number.POSITIVE_INFINITY : parsed;
}

/**
 * Plan cards rendered in the conversation: superseded drafts are hidden so a
 * revised plan replaces its predecessor, while decided plans stay as history.
 */
export function visiblePlanCards(cards: readonly ChatCard[]): ChatCard[] {
  return cards
    .filter((card) => card.kind === "plan" && card.status !== "superseded")
    .sort((a, b) => timeOf(a.created_at) - timeOf(b.created_at));
}

/** The newest question card still waiting for an answer. */
export function latestPendingQuestion(cards: readonly ChatCard[]): ChatCard | null {
  let latest: ChatCard | null = null;
  for (const card of cards) {
    if (card.kind !== "user_question" || card.status !== "pending") continue;
    if (!latest || timeOf(card.created_at) >= timeOf(latest.created_at)) latest = card;
  }
  return latest;
}

/**
 * Splice plan cards into an ordered row list. A plan sits right after the
 * assistant row of the task that produced it (after earlier plans of that
 * task); without such a row it falls back to its creation time.
 */
export function mergePlanCards<T>(
  items: readonly T[],
  plans: readonly ChatCard[],
  describe: (item: T) => TimelineAnchor,
): PlanTimelineEntry<T>[] {
  const entries: PlanTimelineEntry<T>[] = items.map((item) => ({ kind: "item", item }));
  for (const card of plans) {
    let anchor = -1;
    for (let i = entries.length - 1; i >= 0; i--) {
      const entry = entries[i]!;
      if (entry.kind === "item" && describe(entry.item).anchorTaskId === card.task_id) {
        anchor = i;
        break;
      }
    }
    let at: number;
    if (anchor >= 0) {
      at = anchor + 1;
      while (entries[at]?.kind === "plan") at++;
    } else {
      const created = timeOf(card.created_at);
      at = entries.findIndex(
        (entry) => entry.kind === "item" && timeOf(describe(entry.item).createdAt) > created,
      );
      if (at < 0) at = entries.length;
    }
    entries.splice(at, 0, { kind: "plan", card });
  }
  return entries;
}

export function describeChatMessage(message: ChatMessage): TimelineAnchor {
  return {
    anchorTaskId: message.role === "assistant" ? message.task_id ?? null : null,
    createdAt: message.created_at,
  };
}

/**
 * The plan that may still be decided from the composer: only a pending plan
 * that is the last thing in the conversation. Once the member writes anything
 * after it, the plan becomes read-only history.
 */
export function trailingPendingPlan(
  messages: readonly ChatMessage[],
  plans: readonly ChatCard[],
): ChatCard | null {
  const entries = mergePlanCards(
    messages.filter((message) => message.message_kind !== "onboarding_kickoff"),
    plans,
    describeChatMessage,
  );
  const last = entries[entries.length - 1];
  return last?.kind === "plan" && last.card.status === "pending" ? last.card : null;
}

/** First Markdown heading of a plan, used as its one-line summary. */
export function planHeading(markdown: string | undefined): string | null {
  const match = /^#{1,6}\s+(.+?)\s*#*\s*$/m.exec(markdown ?? "");
  return match?.[1]?.trim() || null;
}

/** A few plain-text body lines, headings excluded, for the collapsed message. */
export function planPreview(markdown: string | undefined, lines = 3): string[] {
  const out: string[] = [];
  let inFence = false;
  for (const raw of (markdown ?? "").split("\n")) {
    const line = raw.trim();
    if (line.startsWith("```")) {
      inFence = !inFence;
      continue;
    }
    if (inFence || line === "") continue;
    // Headings are structure, not content; the first one is the title.
    if (/^#{1,6}\s/.test(line)) continue;
    const text = line
      .replace(/^(?:[-*+]|\d+[.)])\s+/, "")
      .replace(/^>\s?/, "")
      .replace(/^\[[ xX]\]\s+/, "")
      .replace(/[*_`]/g, "")
      .trim();
    if (text) out.push(text);
    if (out.length >= lines) break;
  }
  return out;
}
