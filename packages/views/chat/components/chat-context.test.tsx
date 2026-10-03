import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { ChatContextBadge, CompactContextDialog } from "./chat-context";
import enChat from "../../locales/en/chat.json";
import type { ReactNode } from "react";

const { send } = vi.hoisted(() => ({ send: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api: { sendChatMessage: send } }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace" }));
function mount(children: ReactNode) {
  return render(
    <I18nProvider locale="en" resources={{ en: { chat: enChat } }}>
      <QueryClientProvider
        client={
          new QueryClient({
            defaultOptions: {
              queries: { retry: false },
              mutations: { retry: false },
            },
          })
        }
      >
        {children}
      </QueryClientProvider>
    </I18nProvider>,
  );
}
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
describe("native chat context", () => {
  it("shows the window and exact tokens, with threshold colors", () => {
    mount(
      <ChatContextBadge state={{ usage: { used: 420000, window: 1000000 } }} />,
    );
    expect(screen.getByText("42% · 1000K").getAttribute("title")).toBe(
      "420,000 / 1,000,000 tokens",
    );
    cleanup();
    mount(<ChatContextBadge state={{ usage: { used: 81, window: 100 } }} />);
    expect(screen.getByText("81% · 0K").className).toContain("text-warning");
    cleanup();
    mount(<ChatContextBadge state={{ usage: { used: 96, window: 100 } }} />);
    expect(screen.getByText("96% · 0K").className).toContain(
      "text-destructive",
    );
  });
  it("hides unavailable native metrics and unsupported runtimes", () => {
    const empty = mount(<ChatContextBadge />);
    expect(empty.container.textContent).toBe("");
    cleanup();
    const unsupported = mount(
      <ChatContextBadge
        supported={false}
        state={{ usage: { used: 5, window: 100 } }}
      />,
    );
    expect(unsupported.container.textContent).toBe("");
  });
  it("shows automatic or manual compaction and optional native counts", () => {
    mount(<ChatContextBadge state={{ compaction: { status: "started" } }} />);
    expect(screen.getByRole("status").textContent).toBe("Compacting context…");
    cleanup();
    mount(
      <ChatContextBadge
        state={{
          compaction: {
            status: "completed",
            pre_tokens: 22657,
            post_tokens: 1629,
          },
        }}
      />,
    );
    expect(screen.getByRole("status").textContent).toBe(
      "Compacted (22.7K → 1.6K)",
    );
    cleanup();
    mount(<ChatContextBadge state={{ compaction: { status: "completed" } }} />);
    expect(screen.getByRole("status").textContent).toBe("Compacted");
  });
  it("requires confirmation and closes only after enqueue succeeds", async () => {
    let resolve!: () => void;
    send.mockReturnValue(
      new Promise<void>((done) => {
        resolve = done;
      }),
    );
    const close = vi.fn();
    mount(
      <CompactContextDialog sessionId="session" open onOpenChange={close} />,
    );
    expect(
      screen.getByText("Compaction will lose some conversation details."),
    ).toBeTruthy();
    expect(send).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Compact context" }));
    await waitFor(() =>
      expect(send).toHaveBeenCalledWith(
        "session",
        "",
        undefined,
        undefined,
        "compact",
      ),
    );
    expect(close).not.toHaveBeenCalled();
    resolve();
    await waitFor(() => expect(close).toHaveBeenCalledWith(false));
  });
  it("cancel never sends a compaction task", () => {
    const close = vi.fn();
    mount(
      <CompactContextDialog sessionId="session" open onOpenChange={close} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(close).toHaveBeenCalledWith(false);
    expect(send).not.toHaveBeenCalled();
  });
});
