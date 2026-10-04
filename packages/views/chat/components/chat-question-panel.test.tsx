import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { api } from "@multica/core/api";
import type { ChatCard, ChatCardQuestion } from "@multica/core/chat/planning";
import { beforeEach, describe, expect, it, vi } from "vitest";
import enChat from "../../locales/en/chat.json";
import { ChatQuestionPanel } from "./chat-question-panel";

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace" }));
vi.mock("@multica/core/api", () => ({ api: { sendChatMessage: vi.fn() } }));

function question(id: string, header: string, patch: Partial<ChatCardQuestion> = {}): ChatCardQuestion {
  return {
    id, header, text: `${header}?`, selection_mode: "single", required: true,
    options: [
      { id: `${id}-a`, label: `${header} A`, description: "first" },
      { id: `${id}-b`, label: `${header} B`, description: "" },
    ],
    free_text: { allowed: true, secret: false },
    ...patch,
  };
}

function card(questions: ChatCardQuestion[]): ChatCard {
  return {
    id: "card", task_id: "task", kind: "user_question", status: "pending", created_at: "2026-10-04T00:00:00Z",
    payload: { title: "Questions", questions },
  };
}

const THREE = card([
  question("q0", "Scope"),
  question("q1", "Tests", { selection_mode: "multiple" }),
  question("q2", "Deploy"),
]);

function mount(value: ChatCard) {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false } } })}>
      <I18nProvider locale="en" resources={{ en: { chat: enChat } }}>
        <ChatQuestionPanel sessionId="session" card={value} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

function lastDecision() {
  return vi.mocked(api.sendChatMessage).mock.calls.at(-1)?.[3];
}

beforeEach(() => {
  vi.mocked(api.sendChatMessage).mockReset();
  vi.mocked(api.sendChatMessage).mockResolvedValue({ task_id: "next", message_id: "m", queued: false, supports_queue: true, created_at: "now" });
});

describe("ChatQuestionPanel", () => {
  it("shows one question at a time and auto-advances after a single choice", async () => {
    mount(THREE);
    expect(screen.getByText("Question 1 of 3")).toBeInTheDocument();
    expect(screen.getByText("Scope?")).toBeInTheDocument();
    expect(screen.queryByText("Tests?")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("radio", { name: /Scope A/ }));
    expect(await screen.findByText("Tests?")).toBeInTheDocument();
    expect(screen.queryByText("Scope?")).not.toBeInTheDocument();
    expect(screen.getByText("Question 2 of 3")).toBeInTheDocument();
  });

  it("collects multi-select answers with Next, reviews, and submits stable ids", async () => {
    mount(THREE);
    fireEvent.click(screen.getByRole("radio", { name: /Scope B/ }));
    await screen.findByText("Tests?");
    expect(screen.getByRole("button", { name: "Next" })).toBeDisabled();
    fireEvent.click(screen.getByRole("checkbox", { name: /Tests A/ }));
    fireEvent.click(screen.getByRole("checkbox", { name: /Tests B/ }));
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "race tests" } });
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(await screen.findByText("Deploy?")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Skip question" }));
    expect(await screen.findByText("Review your answers", { selector: "div" })).toBeInTheDocument();
    expect(screen.getByText("Tests A, Tests B, race tests")).toBeInTheDocument();
    expect(screen.getByText("Skipped")).toBeInTheDocument();
    expect(api.sendChatMessage).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Submit answers" }));
    await waitFor(() => expect(api.sendChatMessage).toHaveBeenCalledTimes(1));
    expect(lastDecision()).toEqual({
      card_id: "card",
      action: "answer",
      answers: [
        { question_id: "q0", selected_option_ids: ["q0-b"], text: "", skipped: false },
        { question_id: "q1", selected_option_ids: ["q1-a", "q1-b"], text: "race tests", skipped: false },
        { question_id: "q2", selected_option_ids: [], text: "", skipped: true },
      ],
    });
  });

  it("returns to an answered question from its step chip and keeps the edit", async () => {
    mount(THREE);
    fireEvent.click(screen.getByRole("radio", { name: /Scope A/ }));
    await screen.findByText("Tests?");
    // Unanswered later steps are not reachable yet.
    expect(screen.getByRole("button", { name: "Deploy" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Scope" }));
    expect(screen.getByText("Scope?")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /Scope A/ })).toHaveAttribute("aria-checked", "true");
    fireEvent.click(screen.getByRole("radio", { name: /Scope B/ }));
    expect(await screen.findByText("Tests?")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Previous" }));
    expect(screen.getByRole("radio", { name: /Scope B/ })).toHaveAttribute("aria-checked", "true");
  });

  it("submits a single question directly without a review step", async () => {
    mount(card([question("only", "Branch")]));
    expect(screen.queryByRole("button", { name: "Skip all" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("radio", { name: /Branch B/ }));
    await waitFor(() => expect(api.sendChatMessage).toHaveBeenCalledTimes(1));
    expect(lastDecision()).toMatchObject({
      action: "answer",
      answers: [{ question_id: "only", selected_option_ids: ["only-b"], skipped: false }],
    });
  });

  it("dismisses the whole card with Skip all", async () => {
    mount(THREE);
    fireEvent.click(screen.getByRole("button", { name: "Skip all" }));
    await waitFor(() => expect(lastDecision()).toEqual({ card_id: "card", action: "dismiss" }));
  });

  it("collapses with X or Escape without skipping and restores the same step", async () => {
    mount(THREE);
    fireEvent.click(screen.getByRole("radio", { name: /Scope A/ }));
    await screen.findByText("Tests?");
    fireEvent.click(screen.getByRole("button", { name: "Collapse" }));
    expect(screen.getByText("3 questions awaiting your answer")).toBeInTheDocument();
    expect(api.sendChatMessage).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Show questions" }));
    expect(screen.getByText("Tests?")).toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole("region", { name: "Questions from the agent" }), { key: "Escape" });
    expect(screen.getByText("3 questions awaiting your answer")).toBeInTheDocument();
  });

  it("selects with number keys and submits typed answers with Enter", async () => {
    mount(card([question("q0", "Scope"), question("q1", "Name", { options: [], selection_mode: "none" })]));
    fireEvent.keyDown(screen.getByRole("radio", { name: /Scope A/ }), { key: "2" });
    expect(screen.getByRole("radio", { name: /Scope B/ })).toHaveAttribute("aria-checked", "true");
    await screen.findByText("Name?");
    const input = screen.getByRole("textbox");
    fireEvent.change(input, { target: { value: "multica" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(await screen.findByText("Review your answers", { selector: "div" })).toBeInTheDocument();
    expect(screen.getByText("multica")).toBeInTheDocument();
  });

  it("keeps secret replies masked", () => {
    mount(card([question("q0", "Token", { options: [], selection_mode: "none", free_text: { allowed: true, secret: true } })]));
    expect(screen.getByLabelText("Other: write your own reply")).toHaveAttribute("type", "password");
  });

  // Cross-review regressions (QORA-17).
  it("keeps keyboard focus in the panel across auto-advance", async () => {
    const user = userEvent.setup();
    mount(THREE);
    screen.getByRole("radio", { name: /Scope A/ }).focus();
    await user.keyboard("1");
    await screen.findByText("Tests?");
    await user.keyboard("2");
    expect(screen.getByRole("checkbox", { name: /Tests B/ })).toHaveAttribute("aria-checked", "true");
  });

  it("drops a pending single-choice advance once the reader types their own reply", async () => {
    mount(card([question("only", "Branch")]));
    fireEvent.click(screen.getByRole("radio", { name: /Branch A/ }));
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "custom branch" } });
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 250)); });
    expect(api.sendChatMessage).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Submit answers" }));
    await waitFor(() => expect(lastDecision()).toMatchObject({
      answers: [{ question_id: "only", selected_option_ids: [], text: "custom branch", skipped: false }],
    }));
  });

  it("drops a pending advance when the reader navigates away", async () => {
    mount(THREE);
    fireEvent.click(screen.getByRole("radio", { name: /Scope A/ }));
    await screen.findByText("Tests?");
    fireEvent.click(screen.getByRole("button", { name: "Scope" }));
    fireEvent.click(screen.getByRole("radio", { name: /Scope B/ }));
    fireEvent.click(screen.getByRole("button", { name: "Collapse" }));
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 250)); });
    fireEvent.click(screen.getByRole("button", { name: "Show questions" }));
    expect(screen.getByText("Scope?")).toBeInTheDocument();
  });

  it("advances a multi-select question with Enter instead of toggling", async () => {
    const user = userEvent.setup();
    mount(card([question("q0", "Tests", { selection_mode: "multiple" }), question("q1", "Deploy")]));
    const option = screen.getByRole("checkbox", { name: /Tests A/ });
    await user.click(option);
    await user.keyboard("{Enter}");
    expect(screen.getByText("Deploy?")).toBeInTheDocument();
    await user.keyboard("2");
    expect(screen.getByRole("radio", { name: /Deploy B/ })).toHaveAttribute("aria-checked", "true");
  });
});
