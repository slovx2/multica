// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  ChatSessionSchema,
  DirectorySyncRequestSchema,
  DirectorySyncStatusSchema,
} from "./schemas";
describe("chat context API boundary", () => {
  it("drops missing or malformed native usage without inventing a value", () => {
    for (const usage of [
      null,
      { used: 3 },
      { used: -1, window: 100 },
      { used: 1, window: 0 },
      { used: "42", window: 100 },
    ]) {
      const session = ChatSessionSchema.parse({
        id: "session",
        context_state: { usage },
      });
      expect(session.context_state?.usage).toBeNull();
    }
    expect(
      ChatSessionSchema.parse({ id: "session" }).context_state,
    ).toBeUndefined();
  });
  it("rejects malformed sync response objects", () => {
    expect(DirectorySyncRequestSchema.safeParse({ id: 42 }).success).toBe(
      false,
    );
    expect(
      DirectorySyncStatusSchema.safeParse({
        status: "completed",
        result: { status: "updated", updated: "3" },
      }).success,
    ).toBe(false);
    expect(
      DirectorySyncStatusSchema.safeParse({ status: "future" }).success,
    ).toBe(false);
  });
});
