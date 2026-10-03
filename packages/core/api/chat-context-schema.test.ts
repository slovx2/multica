// @vitest-environment node
import { readFileSync } from "node:fs";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import {
  ChatSessionSchema,
  DirectorySyncRequestSchema,
  DirectorySyncStatusSchema,
  SendChatMessageResponseSchema,
} from "./schemas";

// The Go HTTP tests compare actual handler responses with this same fixture.
const contracts = JSON.parse(readFileSync(new URL("../../../server/internal/handler/testdata/chat-context-contract.json", import.meta.url), "utf8"));
afterEach(() => vi.unstubAllGlobals());

describe("chat context wire contracts", () => {
  it("accepts a queued action without a message ID while keeping ordinary sends strict", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(contracts.compact), { status: 201 }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(new ApiClient("https://api.example.test").compactChatContext("session"))
      .resolves.toEqual(contracts.compact);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual({ action: "compact" });
    expect(SendChatMessageResponseSchema.safeParse(contracts.compact).success).toBe(false);
  });

  it("returns the completed sync's nested JSON result", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(contracts.sync))));
    await expect(new ApiClient("https://api.example.test").getChatDirectorySync("session", "sync-id"))
      .resolves.toMatchObject({ status: "completed", result: { status: "updated", updated: 3 } });
  });

  it.each([{}, { task_id: "task-id", created_at: "" }])("rejects malformed action responses", async (body) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(body))));
    await expect(new ApiClient("https://api.example.test").compactChatContext("session"))
      .rejects.toThrow("invalid chat action response");
  });
});
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
