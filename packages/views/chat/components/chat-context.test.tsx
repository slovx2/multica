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
import { ChatContextBadge, CompactContextDialog, ChatDirectorySync } from "./chat-context";
import { projectResourcesOptions } from "@multica/core/projects";
import { runtimeListOptions } from "@multica/core/runtimes";
import enChat from "../../locales/en/chat.json";
import zhChat from "../../locales/zh-Hans/chat.json";
import type { ReactNode } from "react";

const { send, sync, status } = vi.hoisted(() => ({ send: vi.fn(), sync: vi.fn(), status: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api: { compactChatContext: send, syncProjectDirectory: sync, getProjectDirectorySync: status } }));
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
  function syncClient() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
    client.setQueryData(projectResourcesOptions("workspace", "project").queryKey, { total: 1, resources: [{
      id: "resource", project_id: "project", workspace_id: "workspace", label: null,
      position: 0, created_at: "2026-10-03T00:00:00Z", created_by: null,
      resource_type: "local_directory", resource_ref: { local_path: "/fixture", daemon_id: "daemon", auto_sync: "off" },
    }] });
    client.setQueryData(runtimeListOptions("workspace").queryKey, [{
      id: "runtime", daemon_id: "daemon", workspace_id: "workspace", name: "Claude",
      runtime_mode: "local", provider: "claude", launch_header: "", status: "online",
      device_info: "", metadata: {}, owner_id: null, visibility: "private",
      last_seen_at: null, created_at: "2026-10-03T00:00:00Z", updated_at: "2026-10-03T00:00:00Z",
    }]);
    return client;
  }
  function mountSync(running = false, locale: "en" | "zh-Hans" = "en") {
    return render(<I18nProvider locale={locale} resources={{ en: { chat: enChat }, "zh-Hans": { chat: zhChat } }}><QueryClientProvider client={syncClient()}>
      <ChatDirectorySync projectId="project" agentId="agent" runtimeId="runtime" running={running} />
    </QueryClientProvider></I18nProvider>);
  }
  it("syncs the project for the agent without a session and explains the action", async () => {
    sync.mockResolvedValue({ id: "sync" });
    status.mockResolvedValue({ status: "completed", result: { status: "current" } });
    mountSync(false, "zh-Hans");
    const button = screen.getByRole("button", { name: "同步代码" });
    expect(button).toBeEnabled();
    expect(button).toHaveAttribute("title", zhChat.context.sync_hint);
    fireEvent.click(button);
    await waitFor(() => expect(sync).toHaveBeenCalledWith("project", { agent_id: "agent", force: false }));
    await waitFor(() => expect(status).toHaveBeenCalledWith("project", "sync"));
    await waitFor(() => expect(button).toBeEnabled());
  });
  it("asks before syncing while the agent runs and forces only after confirmation", async () => {
    sync.mockResolvedValue({ id: "sync" });
    status.mockResolvedValue({ status: "completed", result: { status: "current" } });
    mountSync(true);
    fireEvent.click(screen.getByRole("button", { name: "Sync code" }));
    expect(await screen.findByText(enChat.context.sync_confirm_running)).toBeVisible();
    expect(sync).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(sync).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Sync code" }));
    fireEvent.click(await screen.findByRole("button", { name: "Sync anyway" }));
    await waitFor(() => expect(sync).toHaveBeenCalledWith("project", { agent_id: "agent", force: true }));
  });
  it("turns a busy directory into the same confirmation and retries with force", async () => {
    sync.mockResolvedValueOnce({ id: "first" }).mockResolvedValueOnce({ id: "second" });
    status.mockImplementation(async (_project: string, id: string) => id === "first"
      ? { status: "completed", result: { status: "skipped", reason: "directory_busy", ahead: 0, behind: 2, updated: 0 } }
      : { status: "completed", result: { status: "updated", ahead: 0, behind: 0, updated: 2 } });
    mountSync(false);
    fireEvent.click(screen.getByRole("button", { name: "Sync code" }));
    expect(await screen.findByText(enChat.context.sync_confirm_busy)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Sync anyway" }));
    await waitFor(() => expect(sync).toHaveBeenLastCalledWith("project", { agent_id: "agent", force: true }));
    await waitFor(() => expect(status).toHaveBeenCalledWith("project", "second"));
  });
  it("keeps the last usage visible beside a compaction failure", () => {
    mount(<ChatContextBadge state={{ usage: { used: 420000, window: 1000000 }, compaction: { status: "failed", error: "No conversation found" } }} />);
    expect(screen.getByText("42% · 1000K")).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent("Context compaction failed");
    expect(screen.getByRole("alert")).toHaveAttribute("title", "No conversation found");
  });

  it("uses full-width parentheses in Chinese compaction counts", () => {
    render(<I18nProvider locale="zh-Hans" resources={{ "zh-Hans": { chat: zhChat } }}>
      <ChatContextBadge state={{ compaction: { status: "completed", pre_tokens: 28567, post_tokens: 1694 } }} />
    </I18nProvider>);
    expect(screen.getByRole("status")).toHaveTextContent("已压缩（28.6K → 1.7K）");
  });
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
