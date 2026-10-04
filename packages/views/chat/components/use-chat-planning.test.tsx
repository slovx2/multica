import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { api } from "@multica/core/api";
import type { ChatCard } from "@multica/core/chat/planning";
import type { ChatMessage } from "@multica/core/types";
import { beforeEach, describe, expect, it, vi } from "vitest";
import enChat from "../../locales/en/chat.json";
import { ChatPlanMessage } from "./chat-plan-message";
import { ChatPlanningComposer, useChatPlanning } from "./use-chat-planning";

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace" }));
vi.mock("@multica/core/api", () => ({ api: { sendChatMessage: vi.fn(), listChatCards: vi.fn() } }));

const PLAN_MD = "# Rework chat cards\nSplit the file into four components\n\n## Steps\n- Add tests";

function plan(patch: Partial<ChatCard> = {}): ChatCard {
  return {
    id: "plan", task_id: "task-1", kind: "plan", status: "pending", created_at: "2026-10-04T10:00:05Z",
    payload: { title: "Plan", markdown: PLAN_MD }, ...patch,
  };
}
const user1: ChatMessage = { id: "u1", chat_session_id: "s", role: "user", content: "plan it", task_id: "task-1", created_at: "2026-10-04T10:00:00Z" };
const reply1: ChatMessage = { id: "a1", chat_session_id: "s", role: "assistant", content: "Here is the plan", task_id: "task-1", created_at: "2026-10-04T10:00:10Z" };
const user2: ChatMessage = { id: "u2", chat_session_id: "s", role: "user", content: "wait", task_id: "task-2", created_at: "2026-10-04T10:01:00Z" };

// Renders the plan rows the way the message list does, plus the composer slot.
function Harness({ messages }: { messages: ChatMessage[] }) {
  const planning = useChatPlanning("s", messages);
  return (
    <>
      <div data-testid="timeline">
        {planning.planCards.map((card) => (
          <ChatPlanMessage key={card.id} card={card} onOpen={planning.openPlanDialog} />
        ))}
      </div>
      <ChatPlanningComposer planning={planning} />
    </>
  );
}

const STATUS = { approve: "approved", reject: "rejected", answer: "answered", dismiss: "dismissed" } as const;

function mount(initial: ChatCard[], messages: ChatMessage[] = [user1, reply1]) {
  // A tiny server: decisions resolve the card, so refetches see the result.
  let cards = initial;
  vi.mocked(api.listChatCards).mockImplementation(async () => cards);
  vi.mocked(api.sendChatMessage).mockImplementation(async (_session, _content, _attachments, decision) => {
    if (decision) cards = cards.map((card) => card.id === decision.card_id ? { ...card, status: STATUS[decision.action] } : card);
    return { task_id: "next", message_id: "m", queued: false, supports_queue: true, created_at: "now" };
  });
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
      <I18nProvider locale="en" resources={{ en: { chat: enChat } }}>
        <Harness messages={messages} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.mocked(api.sendChatMessage).mockReset();
});

describe("chat planning", () => {
  it("renders only the newest plan as a message and hides superseded drafts", async () => {
    mount([plan({ id: "old", status: "superseded", payload: { title: "Plan", markdown: "# Old plan" } }), plan()]);
    const row = await screen.findByRole("button", { name: /Rework chat cards/ });
    expect(row).toHaveTextContent("Pending");
    expect(row).toHaveTextContent("Split the file into four components");
    expect(screen.queryByText("Old plan")).not.toBeInTheDocument();
  });

  it("offers the decision bar only while the pending plan is the last row", async () => {
    const view = mount([plan()]);
    expect(await screen.findByRole("region", { name: "Plan awaiting your decision" })).toBeInTheDocument();
    view.unmount();
    mount([plan()], [user1, reply1, user2]);
    await screen.findByRole("button", { name: /Rework chat cards/ });
    expect(screen.queryByRole("region", { name: "Plan awaiting your decision" })).not.toBeInTheDocument();
  });

  it("approves from the bar and sends feedback as a rejection", async () => {
    mount([plan()]);
    const bar = await screen.findByRole("region", { name: "Plan awaiting your decision" });
    fireEvent.click(within(bar).getByRole("button", { name: "Request changes" }));
    fireEvent.change(within(bar).getByRole("textbox", { name: "Feedback" }), { target: { value: "Add e2e" } });
    fireEvent.click(within(bar).getByRole("button", { name: "Send feedback" }));
    await waitFor(() => expect(api.sendChatMessage).toHaveBeenCalledWith("s", "", undefined, { card_id: "plan", action: "reject", feedback: "Add e2e" }));
    // The decided plan leaves the composer; its message stays as history.
    await waitFor(() => expect(screen.queryByRole("region", { name: "Plan awaiting your decision" })).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: /Rework chat cards/ })).toHaveTextContent("Rejected");
  });

  it("opens the plan in a dialog that closes without deciding", async () => {
    mount([plan()]);
    fireEvent.click(await screen.findByRole("button", { name: /Rework chat cards/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("heading", { name: "Steps" })).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Approve, create/update issues" })).toBeEnabled();
    fireEvent.keyDown(dialog, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(api.sendChatMessage).not.toHaveBeenCalled();
  });

  it("keeps the dialog read-only once the plan is no longer the last row", async () => {
    mount([plan()], [user1, reply1, user2]);
    fireEvent.click(await screen.findByRole("button", { name: /Rework chat cards/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByRole("button", { name: "Approve, create/update issues" })).not.toBeInTheDocument();
    expect(within(dialog).getByText(enChat.planning.read_only)).toBeInTheDocument();
  });

  it("shows the pending question panel and drops it once answered", async () => {
    mount([{
      id: "q", task_id: "task-1", kind: "user_question", status: "pending", created_at: "2026-10-04T10:00:06Z",
      payload: { title: "Questions", questions: [{
        id: "q0", header: "Scope", text: "Which scope?", selection_mode: "single", required: true,
        options: [{ id: "o0", label: "Small", description: "" }], free_text: { allowed: false, secret: false },
      }] },
    }]);
    fireEvent.click(await screen.findByRole("radio", { name: /Small/ }));
    await waitFor(() => expect(api.sendChatMessage).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByText("Which scope?")).not.toBeInTheDocument());
  });
});
