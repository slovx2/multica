// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ChatCard } from "@multica/core/chat/planning";
import type { ChatMessage } from "@multica/core/types";
import {
  latestPendingQuestion,
  planHeading,
  planPreview,
  trailingPendingPlan,
  visiblePlanCards,
} from "./plan-timeline";

function card(id: string, patch: Partial<ChatCard> = {}): ChatCard {
  return {
    id, task_id: "task-1", kind: "plan", status: "pending", created_at: "2026-10-04T10:00:05Z",
    payload: { title: "Plan", markdown: "# Ship it\nStep one" }, ...patch,
  };
}
function message(id: string, role: "user" | "assistant", created_at: string, task_id: string | null = null): ChatMessage {
  return { id, chat_session_id: "s", role, content: id, task_id, created_at };
}

const user1 = message("u1", "user", "2026-10-04T10:00:00Z", "task-1");
const reply1 = message("a1", "assistant", "2026-10-04T10:00:10Z", "task-1");
const user2 = message("u2", "user", "2026-10-04T10:01:00Z", "task-2");

describe("plan timeline", () => {
  it("hides superseded drafts and keeps decided plans in creation order", () => {
    const cards = [
      card("new", { created_at: "2026-10-04T10:02:00Z" }),
      card("old", { status: "superseded", created_at: "2026-10-04T10:01:00Z" }),
      card("done", { status: "approved", created_at: "2026-10-04T09:00:00Z" }),
      card("q", { kind: "user_question" }),
    ];
    expect(visiblePlanCards(cards).map((c) => c.id)).toEqual(["done", "new"]);
  });

  it("is actionable only while a pending plan is the last row", () => {
    const plan = card("p");
    expect(trailingPendingPlan([user1, reply1], [plan])?.id).toBe("p");
    // Anchored after its task's reply even though it was created earlier.
    expect(trailingPendingPlan([user1, reply1, user2], [plan])).toBeNull();
    expect(trailingPendingPlan([user1, reply1], [card("p", { status: "approved" })])).toBeNull();
  });

  it("falls back to creation time when the producing reply is not persisted", () => {
    const plan = card("p", { task_id: "task-live", created_at: "2026-10-04T10:00:30Z" });
    expect(trailingPendingPlan([user1, reply1], [plan])?.id).toBe("p");
    expect(trailingPendingPlan([user1, reply1, user2], [plan])).toBeNull();
  });

  it("picks the newest pending question", () => {
    const questions = [
      card("q1", { kind: "user_question", created_at: "2026-10-04T10:00:00Z" }),
      card("q2", { kind: "user_question", created_at: "2026-10-04T10:05:00Z" }),
      card("q3", { kind: "user_question", status: "answered", created_at: "2026-10-04T10:09:00Z" }),
    ];
    expect(latestPendingQuestion(questions)?.id).toBe("q2");
    expect(latestPendingQuestion([])).toBeNull();
  });

  it("summarises a plan by its first heading and plain-text preview", () => {
    const markdown = "Intro line\n## Rework chat cards\n- **Split** the file\n### Steps\n```ts\ncode()\n```\n1. Add tests\n> keep scope";
    expect(planHeading(markdown)).toBe("Rework chat cards");
    expect(planHeading("no heading")).toBeNull();
    expect(planPreview(markdown)).toEqual(["Intro line", "Split the file", "Add tests"]);
  });
});
