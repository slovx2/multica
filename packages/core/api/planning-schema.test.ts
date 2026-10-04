// @vitest-environment node
import { describe, expect, it } from "vitest";
import { chatCardsSchema, planningLinksSchema } from "./planning-schema";
import { ChatSessionSchema } from "./schemas";

describe("planning response boundaries", () => {
  it("defaults old/malformed session modes to off", () => {
    expect(ChatSessionSchema.parse({ id: "s" }).plan_mode).toBe(false);
    expect(ChatSessionSchema.parse({ id: "s", plan_mode: "true" }).plan_mode).toBe(false);
  });
  it("defaults old and malformed execution overrides without rejecting the session", () => {
    for (const execution_overrides of [undefined, null, "high", []]) {
      expect(ChatSessionSchema.parse({ id: "s", execution_overrides }).execution_overrides).toEqual({});
    }
    expect(ChatSessionSchema.parse({ id: "s", execution_overrides: { thinking_level: 1, service_tier: true } }).execution_overrides).toEqual({ thinking_level: "", service_tier: "" });
    expect(ChatSessionSchema.parse({ id: "s", execution_overrides: { thinking_level: "high", service_tier: "default" } }).execution_overrides).toEqual({ thinking_level: "high", service_tier: "default" });
  });
  it("makes unknown card statuses inert and rejects malformed questions", () => {
    const card = { id: "c", task_id: "t", kind: "plan", status: "future", payload: { title: "Plan" } };
    expect(chatCardsSchema.parse([card])[0]?.status).toBe("superseded");
    expect(chatCardsSchema.parse([card, { ...card, payload: { title: "Question", questions: [{ id: 4 }] } }])).toHaveLength(1);
    expect(planningLinksSchema.safeParse([{ title: "missing id" }]).success).toBe(false);
  });
  it("accepts dismissed cards and skipped answers, defaulting both for older servers", () => {
    const base = { id: "c", task_id: "t", kind: "user_question", payload: { title: "Questions" } };
    const [dismissed] = chatCardsSchema.parse([{ ...base, status: "dismissed", response: { action: "dismiss" } }]);
    expect(dismissed?.status).toBe("dismissed");
    expect(dismissed?.response?.action).toBe("dismiss");
    const [answered] = chatCardsSchema.parse([{ ...base, status: "answered", created_at: "2026-10-04T00:00:00Z", response: { action: "answer", answers: [{ question_id: "q0" }, { question_id: "q1", skipped: true }] } }]);
    expect(answered?.created_at).toBe("2026-10-04T00:00:00Z");
    expect(answered?.response?.answers).toEqual([
      { question_id: "q0", selected_option_ids: [], text: "", skipped: false },
      { question_id: "q1", selected_option_ids: [], text: "", skipped: true },
    ]);
    expect(chatCardsSchema.parse([{ ...base, status: "pending" }])[0]?.created_at).toBe("");
  });
});
