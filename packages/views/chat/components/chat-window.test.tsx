import { StrictMode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createChatStore, registerChatStore, useChatStore } from "@multica/core/chat";
import { chatKeys } from "@multica/core/chat/queries";
import { projectKeys } from "@multica/core/projects/queries";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { ChatWindow } from "./chat-window";

const navigation = vi.hoisted(() => ({
  pathname: "/acme/projects/project-a",
  searchParams: new URLSearchParams(),
}));
vi.mock("../../navigation", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../navigation")>(),
  useNavigation: () => navigation,
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: null }) => unknown) => selector({ user: null }),
}));
vi.mock("@multica/core/agents", () => ({
  isAgentRuntimeBound: () => true,
  useAgentPresenceDetail: () => "loading",
  useCustomizeConversationStartersHref: () => null,
  useWorkspaceAgentAvailability: () => "loading",
}));
vi.mock("../../i18n", () => ({
  useT: () => ({ t: () => "Chat" }),
  useLocale: () => "en",
}));
vi.mock("./use-chat-context-items", async (importOriginal) => ({
  ...await importOriginal<typeof import("./use-chat-context-items")>(),
  useChatContextItems: () => [],
}));
vi.mock("./use-chat-project-context-support", () => ({ useChatProjectContextSupport: () => true }));
vi.mock("./use-chat-draft-restore", () => ({ useChatDraftRestore: () => ({}) }));
vi.mock("./chat-empty-state", () => ({ EmptyState: () => null }));
vi.mock("./chat-cards", () => ({ ChatCards: () => null }));
vi.mock("./chat-message-list", () => ({ ChatMessageList: () => null, ChatMessageSkeleton: () => null }));
// Observe the actual window's composer props and callbacks without mounting
// the rich-text editor. Route initialization and New chat run production code.
vi.mock("./chat-input", () => ({
  ChatInput: ({ projectId, onProjectChange }: {
    projectId: string | null;
    onProjectChange: (id: string | null) => void;
  }) => <>
    <output aria-label="Project">{projectId ?? "No project"}</output>
    <button onClick={() => onProjectChange("project-b")}>Change project</button>
    <button onClick={() => onProjectChange(null)}>Clear project</button>
  </>,
}));

beforeEach(() => {
  localStorage.clear();
  registerChatStore(createChatStore({ storage: localStorage }));
  navigation.pathname = "/acme/projects/project-a";
});

function renderWindow(savedProject?: string | null) {
  const client = new QueryClient({ defaultOptions: { queries: {
    retry: false, staleTime: Infinity, refetchOnMount: false,
  } } });
  client.setQueryData(workspaceKeys.agents("ws-1"), []);
  client.setQueryData(workspaceKeys.members("ws-1"), []);
  client.setQueryData(projectKeys.list("ws-1"), {
    projects: ["project-a", "project-b"].map((id) => ({ id, title: id })),
  });
  client.setQueryData(chatKeys.sessions("ws-1"), savedProject === undefined ? [] : [{
    id: "saved-session", agent_id: "agent-1", project_id: savedProject, title: "Saved", status: "active",
  }]);
  client.setQueryData(chatKeys.pendingTasks("ws-1"), { tasks: [] });
  if (savedProject !== undefined) {
    useChatStore.getState().setActiveSession("saved-session");
    client.setQueryData(chatKeys.pendingTask("saved-session"), null);
    client.setQueryData(chatKeys.messagesPage("saved-session"), { pages: [{ messages: [] }], pageParams: [null] });
  }
  const window = () => <StrictMode><QueryClientProvider client={client}><ChatWindow /></QueryClientProvider></StrictMode>;
  const view = render(window());
  act(() => useChatStore.getState().setOpen(true));
  return {
    ...view,
    refresh: () => view.rerender(window()),
    newChat: () => {
      const button = view.container.querySelector(".lucide-plus")?.closest("button");
      expect(button).toBeTruthy();
      fireEvent.click(button!);
    },
  };
}

describe("floating ChatWindow project context", () => {
  it("selects the route project on opening a draft and keeps manual changes", () => {
    const view = renderWindow();
    expect(screen.getByLabelText("Project")).toHaveTextContent("project-a");
    fireEvent.click(screen.getByRole("button", { name: "Change project" }));
    expect(screen.getByLabelText("Project")).toHaveTextContent("project-b");
    fireEvent.click(screen.getByRole("button", { name: "Clear project" }));
    expect(screen.getByLabelText("Project")).toHaveTextContent("No project");
    view.newChat();
    expect(screen.getByLabelText("Project")).toHaveTextContent("project-a");
  });

  it.each(["project-b", null])("keeps the existing session's project (%s) until New chat", (savedProject) => {
    const view = renderWindow(savedProject);
    expect(screen.getByLabelText("Project")).toHaveTextContent(savedProject ?? "No project");
    expect(useChatStore.getState().selectedProjectId).toBeNull();
    view.newChat();
    expect(useChatStore.getState().activeSessionId).toBeNull();
    expect(screen.getByLabelText("Project")).toHaveTextContent("project-a");
  });

  it("does not replace an open draft's project on navigation, but New chat uses the new route", () => {
    const view = renderWindow();
    navigation.pathname = "/acme/projects/project-b";
    view.refresh();
    expect(screen.getByLabelText("Project")).toHaveTextContent("project-a");
    view.newChat();
    expect(screen.getByLabelText("Project")).toHaveTextContent("project-b");
  });

  it("preserves the project explicitly chosen when leaving a saved session for a new draft", () => {
    renderWindow(null);
    fireEvent.click(screen.getByRole("button", { name: "Change project" }));
    expect(useChatStore.getState().activeSessionId).toBeNull();
    expect(screen.getByLabelText("Project")).toHaveTextContent("project-b");
  });

  it.each(["/acme/projects", "/acme/issues/issue-a"])("does not infer a project from %s", (pathname) => {
    navigation.pathname = pathname;
    renderWindow();
    expect(screen.getByLabelText("Project")).toHaveTextContent("No project");
  });
});
