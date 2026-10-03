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
});
