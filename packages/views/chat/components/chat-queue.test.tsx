import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { describe, expect, it, vi } from "vitest";
import type { ChatQueuedTask } from "@multica/core/types";
import enChat from "../../locales/en/chat.json";
import { ChatQueue } from "./chat-queue";

const { toastError } = vi.hoisted(() => ({ toastError: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: toastError } }));

const TEST_RESOURCES = { en: { chat: enChat } };

function renderQueue(
  headStatus = "running",
  sendNowDisabled = false,
  action?: "compact",
  { steerable = true, receipt = {} }: {
    steerable?: boolean;
    receipt?: Pick<ChatQueuedTask, "supplement_status" | "supplement_failure_reason">;
  } = {},
) {
  const callbacks = {
    onSendNow: vi.fn<(taskId: string) => Promise<void>>().mockResolvedValue(),
    onEdit: vi.fn<(taskId: string) => Promise<void>>().mockResolvedValue(),
    onRemove: vi.fn<(taskId: string) => Promise<void>>().mockResolvedValue(),
    onClear: vi.fn<() => Promise<void>>().mockResolvedValue(),
  };
  const ui = (tasks: ChatQueuedTask[]) => (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <ChatQueue
        headStatus={headStatus}
        sendNowDisabled={sendNowDisabled}
        steerable={steerable}
        tasks={tasks}
        {...callbacks}
      />
    </I18nProvider>
  );
  const tasks = (first: Partial<ChatQueuedTask> = receipt): ChatQueuedTask[] => [
    {
      task_id: "task-2",
      status: "queued",
      content: "First follow-up",
      action,
      ...first,
      created_at: "2026-07-01T00:01:00Z",
    },
    {
      task_id: "task-3",
      status: "queued",
      content: "",
      created_at: "2026-07-01T00:02:00Z",
    },
  ];
  const view = render(ui(tasks()));
  return {
    ...callbacks,
    container: view.container,
    rerenderWith: (first: Partial<ChatQueuedTask>) => view.rerender(ui(tasks(first))),
  };
}

describe("ChatQueue", () => {
  it("labels compaction actions and prevents editing them as messages", async () => {
    const actions = renderQueue("running", false, "compact");
    expect(screen.getByText("Compact context")).toBeInTheDocument();
    const steer = screen.getAllByRole("button", { name: "Steer" })[0]!;
    expect(steer).toBeDisabled();
    fireEvent.click(steer);
    expect(actions.onSendNow).not.toHaveBeenCalled();
    fireEvent.click(screen.getAllByLabelText("More queue actions")[0]!);
    const edit = await screen.findByRole("menuitem", { name: "Edit queued message" });
    expect(edit).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(edit);
    expect(actions.onEdit).not.toHaveBeenCalled();
  });

  it("renders a standalone queue card without a separate header", () => {
    const { container } = renderQueue();

    expect(screen.getByRole("region", { name: "2 queued messages" })).toBeInTheDocument();
    expect(screen.queryByText("2 queued messages")).not.toBeInTheDocument();
    expect(screen.getByText("First follow-up")).toBeInTheDocument();
    expect(screen.getByText("Queued message")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Steer" })).toHaveLength(2);
    expect(screen.getAllByLabelText("Remove queued message")).toHaveLength(2);
    expect(screen.getAllByLabelText("More queue actions")).toHaveLength(2);
    expect(screen.queryByRole("button", { name: "Clear all" })).not.toBeInTheDocument();

    const shell = container.querySelector('[data-slot="chat-queue-shell"]');
    const queue = container.querySelector('[data-slot="chat-queue"]');
    // Tucked stack, owned entirely by the queue: it slides under the composer
    // (negative margin + z-0) while the composer's chrome stays untouched.
    expect(shell).toHaveClass("z-0", "-mb-3");
    expect(queue).toHaveClass(
      "rounded-lg",
      "border-surface-border",
      "bg-surface",
      "pb-4",
    );
    expect(container.querySelectorAll('[data-slot="chat-queue-row"]')).toHaveLength(2);
    expect(container.querySelectorAll('[data-slot="chat-queue-item-icon"]')).toHaveLength(2);
  });

  it("runs steer, remove, and overflow actions against the selected queue state", async () => {
    const actions = renderQueue();

    fireEvent.click(screen.getAllByRole("button", { name: "Steer" })[1]!);
    await waitFor(() => expect(actions.onSendNow).toHaveBeenCalledWith("task-3"));

    fireEvent.click(screen.getAllByLabelText("More queue actions")[0]!);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Edit queued message" }));
    await waitFor(() => expect(actions.onEdit).toHaveBeenCalledWith("task-2"));

    fireEvent.click(screen.getAllByLabelText("Remove queued message")[1]!);
    await waitFor(() => expect(actions.onRemove).toHaveBeenCalledWith("task-3"));

    fireEvent.click(screen.getAllByLabelText("More queue actions")[1]!);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Clear all" }));
    await waitFor(() => expect(actions.onClear).toHaveBeenCalledTimes(1));
  });

  it("disables send-now until the current positional head is claimable", () => {
    const actions = renderQueue("queued");

    const buttons = screen.getAllByRole("button", {
      name: "Steer is available after the current reply starts",
    });
    expect(buttons).toHaveLength(2);
    for (const button of buttons) expect(button).toBeDisabled();
    expect(actions.onSendNow).not.toHaveBeenCalled();
  });

  it("keeps long queues bounded and blocks duplicate actions while one is pending", async () => {
    let finishClear: (() => void) | undefined;
    const actions = renderQueue();
    actions.onClear.mockReturnValue(new Promise<void>((resolve) => {
      finishClear = resolve;
    }));

    const scroller = actions.container.querySelector('[data-slot="chat-queue-list"]');
    expect(scroller).toHaveClass("max-h-40");

    const clearTrigger = screen.getAllByLabelText("More queue actions")[0]!;
    fireEvent.click(clearTrigger);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Clear all" }));
    await waitFor(() => {
      expect(clearTrigger.querySelector(".animate-spin")).toBeInTheDocument();
      for (const button of screen.getAllByRole("button")) {
        expect(button).toBeDisabled();
      }
    });

    finishClear?.();
    await waitFor(() => {
      expect(clearTrigger.querySelector(".animate-spin")).not.toBeInTheDocument();
      for (const button of screen.getAllByRole("button")) {
        expect(button).toBeEnabled();
      }
    });
    expect(actions.onClear).toHaveBeenCalledTimes(1);
  });
});

// MUL-6380: steering a queued message dispatches it now, so it has to clear the
// same invoke gate as a fresh send. When the caller has lost permission to run
// the agent, a live-looking Steer button just walks them into a 403.
describe("ChatQueue send-now gating", () => {
  it("blocks Steer when the caller may no longer invoke the agent", async () => {
    const { onSendNow } = renderQueue("running", true);

    // The label must state the real reason: "wait for the reply to start" would
    // send the user waiting for something waiting cannot fix.
    const steer = screen.getAllByRole("button", {
      name: "You no longer have permission to run this agent",
    })[0]!;
    expect(steer).toBeDisabled();
    fireEvent.click(steer);
    await waitFor(() => expect(onSendNow).not.toHaveBeenCalled());
  });

  it("leaves Steer available when the head task is dispatchable and permitted", () => {
    renderQueue("running", false);

    expect(screen.getAllByRole("button", { name: "Steer" })[0]!).not.toBeDisabled();
  });
});

describe("ChatQueue steering", () => {
  it("offers stop-and-send when the running reply cannot take input mid-turn", async () => {
    const { onSendNow } = renderQueue("running", false, undefined, { steerable: false });
    expect(screen.queryByRole("button", { name: "Steer" })).not.toBeInTheDocument();
    const stop = screen.getAllByRole("button", { name: "Stop and send" })[0]!;
    expect(stop.parentElement).toHaveAttribute("title", enChat.queue.steer_stop_hint);
    fireEvent.click(stop);
    await waitFor(() => expect(onSendNow).toHaveBeenCalledWith("task-2"));
  });

  it("shows a delivering row as steering, blocks editing it again, and still allows removal", async () => {
    const { onRemove, onEdit } = renderQueue("running", false, undefined, {
      receipt: { supplement_status: "delivering" },
    });
    expect(screen.getByRole("status")).toHaveTextContent("Steering…");
    expect(screen.getAllByRole("button", { name: "Steer" })).toHaveLength(1);
    fireEvent.click(screen.getAllByLabelText("More queue actions")[0]!);
    const edit = await screen.findByRole("menuitem", { name: "Edit queued message" });
    expect(edit).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(edit);
    expect(onEdit).not.toHaveBeenCalled();
    fireEvent.click(screen.getAllByLabelText("Remove queued message")[0]!);
    await waitFor(() => expect(onRemove).toHaveBeenCalledWith("task-2"));
  });

  it("announces a delivery that fails on screen once and keeps the row steerable", () => {
    toastError.mockClear();
    const view = renderQueue("running", false, undefined, {
      receipt: { supplement_status: "delivering" },
    });
    expect(toastError).not.toHaveBeenCalled();
    view.rerenderWith({ supplement_status: "failed", supplement_failure_reason: "turn_ended" });
    view.rerenderWith({ supplement_status: "failed", supplement_failure_reason: "turn_ended" });
    expect(toastError).toHaveBeenCalledTimes(1);
    expect(toastError).toHaveBeenCalledWith(
      "Couldn't steer (the reply already ended). The message stays in the queue.",
    );
    expect(screen.getAllByRole("button", { name: "Steer" })).toHaveLength(2);
  });

  it("does not replay a failure that was already settled when the queue loaded", () => {
    toastError.mockClear();
    renderQueue("running", false, undefined, {
      receipt: { supplement_status: "failed", supplement_failure_reason: "timeout" },
    });
    expect(toastError).not.toHaveBeenCalled();
  });
});
