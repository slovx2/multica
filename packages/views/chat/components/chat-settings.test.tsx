import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import {
  useChatStore,
  createChatStore,
  registerChatStore,
} from "@multica/core/chat";
import {
  ChatSettings,
  ChatSettingsTags,
  ChatSettingsMenu,
} from "./chat-settings";
import { ChatAddMenu } from "./chat-add-menu";
import enChat from "../../locales/en/chat.json";
import enCommon from "../../locales/en/common.json";

const mocks = vi.hoisted(() => ({
  provider: "codex",
  update: vi.fn(),
  get: vi.fn(),
  updateAgent: vi.fn(),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws" }));
vi.mock("@multica/core/api", () => ({
  api: {
    updateChatSession: mocks.update,
    getChatSession: mocks.get,
    updateAgent: mocks.updateAgent,
  },
}));
vi.mock("@multica/core/runtimes", () => ({
  runtimeListOptions: () => ({
    queryKey: ["runtimes"],
    queryFn: async () => [
      { id: "rt", provider: mocks.provider, status: "online" },
    ],
  }),
  runtimeModelsOptions: () => ({
    queryKey: ["models"],
    queryFn: async () => ({
      models: [
        {
          id: "gpt-test",
          thinking: { supported_levels: [{ value: "high", label: "High" }] },
          service_tiers: [{ id: "priority", name: "Fast" }],
        },
      ],
    }),
  }),
}));

function mount(sessionId: string | null = null, model = "gpt-test") {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <I18nProvider
      locale="en"
      resources={{ en: { chat: enChat, common: enCommon } }}
    >
      <QueryClientProvider client={qc}>
        <ChatSettings sessionId={sessionId} runtimeId="rt" model={model}>
          {(settings) => (
            <>
              <div data-testid="tags">
                <ChatSettingsTags settings={settings} />
              </div>
              <ChatAddMenu
                extraItems={<ChatSettingsMenu settings={settings} />}
              />
            </>
          )}
        </ChatSettings>
      </QueryClientProvider>
    </I18nProvider>,
  );
}

async function openMenu() {
  fireEvent.click(
    screen.getByRole("button", { name: enChat.input.add_tooltip }),
  );
  await screen.findByRole("menuitem", { name: "Plan" });
}

describe("chat session settings", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.provider = "codex";
    registerChatStore(
      createChatStore({
        storage: {
          getItem: () => null,
          setItem: () => {},
          removeItem: () => {},
        },
      }),
    );
  });
  afterEach(cleanup);

  it("has no persistent plan tag, enables it from +, and exits through ×", async () => {
    mount();
    expect(screen.getByTestId("tags")).toBeEmptyDOMElement();
    await openMenu();
    const plan = screen.getByRole("menuitem", { name: "Plan" });
    await waitFor(() =>
      expect(plan).not.toHaveAttribute("aria-disabled", "true"),
    );
    fireEvent.click(plan);
    expect(
      await screen.findByRole("button", { name: "Exit plan" }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Exit plan" }));
    expect(screen.getByTestId("tags")).toBeEmptyDOMElement();
  });

  it("selects catalog values, displays a compact tag, and clears draft overrides", async () => {
    mount();
    await openMenu();
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "Thinking level" }),
    );
    fireEvent.click(await screen.findByRole("menuitem", { name: "High" }));
    expect(useChatStore.getState().draftExecutionOverrides).toEqual({
      thinking_level: "high",
    });
    await openMenu();
    fireEvent.click(screen.getByRole("menuitem", { name: "Service tier" }));
    expect(
      await screen.findByRole("menuitem", { name: "Default (inherit)" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: "Standard" }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("menuitem", { name: "Fast" }));
    expect(await screen.findByText("high · Fast")).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "Clear chat parameters" }),
    );
    expect(useChatStore.getState().draftExecutionOverrides).toEqual({});
    expect(mocks.updateAgent).not.toHaveBeenCalled();
  });

  it.each([
    ["claude", "gpt-test"],
    ["codex", ""],
    ["codex", "other-model"],
  ])("hides tiers for %s / %s", async (provider, model) => {
    mocks.provider = provider;
    mount(null, model);
    await openMenu();
    expect(
      screen.queryByRole("menuitem", { name: "Service tier" }),
    ).not.toBeInTheDocument();
  });

  it("updates only the current session and keeps saved settings when a write fails", async () => {
    mocks.get.mockResolvedValue({
      id: "session",
      plan_mode: true,
      execution_overrides: { thinking_level: "high", service_tier: "priority" },
    });
    mocks.update.mockRejectedValue(new Error("offline"));
    mount("session");
    const clear = await screen.findByRole("button", {
      name: "Clear chat parameters",
    });
    fireEvent.click(clear);
    await waitFor(() =>
      expect(mocks.update).toHaveBeenCalledWith("session", {
        execution_overrides: {},
      }),
    );
    expect(await screen.findByText("high · Fast")).toBeInTheDocument();
    expect(mocks.updateAgent).not.toHaveBeenCalled();
    expect(useChatStore.getState().draftExecutionOverrides).toEqual({});
  });

  it("clears the draft overrides when moving to another chat", () => {
    mount();
    act(() =>
      useChatStore
        .getState()
        .setDraftExecutionOverrides({ thinking_level: "high" }),
    );
    expect(
      screen.getByRole("button", { name: "Chat parameters" }),
    ).toBeInTheDocument();
    act(() => useChatStore.getState().setActiveSession("another-session"));
    expect(useChatStore.getState().draftExecutionOverrides).toEqual({});
  });
});
