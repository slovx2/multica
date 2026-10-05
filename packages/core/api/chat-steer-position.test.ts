// @vitest-environment node
import { describe, expect, it } from "vitest";
import { ChatMessageListSchema, ChatMessagesPageSchema } from "./schemas";

const message = { id: "guidance", chat_session_id: "session", role: "user", task_id: "running" };

describe("chat steering position contract", () => {
  it.each([0, 7, 2147483647, null])("preserves position %s in lists and pages", (seq) => {
    const input = { ...message, steer_after_seq: seq };
    expect(ChatMessageListSchema.parse([input])[0]?.steer_after_seq).toBe(seq);
    expect(ChatMessagesPageSchema.parse({ messages: [input] }).messages[0]?.steer_after_seq).toBe(seq);
  });
  it.each([undefined, -1, 1.5, "7", {}, true])("degrades absent or invalid position %s without losing the message", (seq) => {
    const parsed = ChatMessageListSchema.parse([{ ...message, steer_after_seq: seq }])[0];
    expect(parsed?.id).toBe("guidance");
    expect(parsed?.steer_after_seq ?? null).toBeNull();
  });
});
