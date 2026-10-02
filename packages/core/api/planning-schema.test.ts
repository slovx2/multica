// @vitest-environment node
import { describe, expect, it } from "vitest";
import { chatCardsSchema, planningLinksSchema } from "./planning-schema";
import { ChatSessionSchema } from "./schemas";

describe("planning response boundaries", () => {
  it("defaults old/malformed session modes to off", () => {
    expect(ChatSessionSchema.parse({ id: "s" }).plan_mode).toBe(false);
    expect(ChatSessionSchema.parse({ id: "s", plan_mode: "true" }).plan_mode).toBe(false);
  });
  it("makes unknown card statuses inert and rejects malformed questions", () => {
    const card = { id: "c", task_id: "t", kind: "plan", status: "future", payload: { title: "Plan" } };
    expect(chatCardsSchema.parse([card])[0]?.status).toBe("superseded");
    expect(chatCardsSchema.safeParse([{ ...card, payload: { title: "Question", questions: [{ id: 4 }] } }]).success).toBe(false);
    expect(planningLinksSchema.safeParse([{ title: "missing id" }]).success).toBe(false);
  });
});
