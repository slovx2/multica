import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { api } from "@multica/core/api";
import type { ChatCard } from "@multica/core/chat/planning";
import { beforeEach, describe, expect, it, vi } from "vitest";
import enChat from "../../locales/en/chat.json";
import { ChatInteractionCard } from "./chat-cards";

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace" }));
vi.mock("@multica/core/api", () => ({ api: { sendChatMessage: vi.fn() } }));

const plan: ChatCard = { id: "card", task_id: "task", kind: "plan", status: "pending", payload: { title: "Plan", markdown: "# Keep the plan\nAdd tests" } };
function mount(card: ChatCard) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false } } })}>
    <I18nProvider locale="en" resources={{ en: { chat: enChat } }}><ChatInteractionCard sessionId="session" card={card} /></I18nProvider>
  </QueryClientProvider>);
}
beforeEach(() => { vi.mocked(api.sendChatMessage).mockReset(); vi.mocked(api.sendChatMessage).mockResolvedValue({ task_id: "next", message_id: "m", queued: false, supports_queue: true, created_at: "now" }); });

describe("planning cards", () => {
  it("approves through an issue-only decision and disables while pending", async () => {
    vi.mocked(api.sendChatMessage).mockImplementation(() => new Promise(() => {}));
    mount(plan);
    expect(screen.getByRole("heading", { name: "Keep the plan" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Approve, create/update issues" }));
    await waitFor(() => expect(api.sendChatMessage).toHaveBeenCalledWith("session", "", undefined, expect.objectContaining({ card_id: "card", action: "approve" })));
    expect(screen.getByRole("button", { name: "Reject" })).toBeDisabled();
  });
  it("sends rejection feedback and preserves it after failure", async () => {
    vi.mocked(api.sendChatMessage).mockRejectedValue(new Error("Offline"));
    mount(plan); fireEvent.change(screen.getByRole("textbox", { name: "Feedback" }), { target: { value: "Add integration tests" } });
    fireEvent.click(screen.getByRole("button", { name: "Reject" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Offline");
    expect(screen.getByRole("textbox", { name: "Feedback" })).toHaveValue("Add integration tests");
    expect(api.sendChatMessage).toHaveBeenCalledWith("session", "", undefined, expect.objectContaining({ action: "reject", feedback: "Add integration tests" }));
  });
  it("sends stable option ids and free text for multiple selection", async () => {
    mount({ ...plan, kind: "user_question", payload: { title: "Questions", questions: [{ id: "q0", header: "Tests", text: "Which tests?", selection_mode: "multiple", options: [{ id: "o0", label: "Unit", description: "Fast" }, { id: "o1", label: "Integration", description: "Full path" }], free_text: { allowed: true, secret: false }, required: true }] } });
    fireEvent.click(screen.getByRole("checkbox", { name: /Unit/ })); fireEvent.click(screen.getByRole("checkbox", { name: /Integration/ }));
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "also timeout" } });
    fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
    await waitFor(() => expect(api.sendChatMessage).toHaveBeenCalledWith("session", "", undefined, expect.objectContaining({ answers: [{ question_id: "q0", selected_option_ids: ["o0", "o1"], text: "also timeout" }] })));
  });
  it("does not offer approval for superseded plans", () => {
    mount({ ...plan, status: "superseded" });
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.getByText("Superseded")).toBeInTheDocument();
  });

  it("keeps decisions disabled after success while the parent still has a pending card", async () => {
    mount(plan);
    fireEvent.change(screen.getByRole("textbox", { name: "Feedback" }), { target: { value: "Include timeout tests" } });
    const approve = screen.getByRole("button", { name: "Approve, create/update issues" });
    fireEvent.click(approve);
    await waitFor(() => expect(api.sendChatMessage).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(approve).toBeDisabled());
    fireEvent.click(approve);
    expect(api.sendChatMessage).toHaveBeenCalledTimes(1);
    expect(api.sendChatMessage).toHaveBeenCalledWith("session", "", undefined, expect.objectContaining({ action: "approve", feedback: "Include timeout tests" }));
  });

  it("shows saved answers after reopening an answered card", () => {
    mount({ ...plan, kind: "user_question", status: "answered", payload: {
      title: "Saved question", questions: [{ id: "q", header: "Tests", text: "Choose", selection_mode: "single", options: [{ id: "unit", label: "Unit", description: "" }], free_text: { allowed: true, secret: false }, required: true }],
    }, response: { action: "answer", answers: [{ question_id: "q", selected_option_ids: ["unit"], text: "Also race tests" }] } });
    expect(screen.getByRole("radio", { name: "Unit" })).toBeChecked();
    expect(screen.getByRole("textbox")).toHaveValue("Also race tests");
    expect(screen.getByRole("textbox")).toBeDisabled();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});
