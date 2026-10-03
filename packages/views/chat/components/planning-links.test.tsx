import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { ChatPlanningIssues } from "./planning-links";
import enChat from "../../locales/en/chat.json";

const mocks = vi.hoisted(() => ({ links: [] as { id: string; title: string }[] }));
vi.mock("@multica/core/chat/planning", () => ({ usePlanningLinks: () => ({ data: mocks.links }) }));
vi.mock("@multica/core/paths", () => ({ useWorkspacePaths: () => ({ issueDetail: (id: string) => `/issues/${id}` }) }));
vi.mock("../../navigation", () => ({ AppLink: (props: React.ComponentProps<"a">) => <a {...props} /> }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws" }));
vi.mock("@multica/core/issue-statuses/hooks", () => ({ useIssueStatuses: () => ({ iconOf: () => null, colorOf: () => null }) }));
vi.mock("@multica/core/issues/queries", () => ({ issueListOptions: () => ({}), issueDetailOptions: () => ({ detail: true }) }));
vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { detail?: boolean }) => ({ data: options.detail ? undefined : mocks.links.map((link, index) => ({
    ...link, identifier: `QORA-${10 - index}`, status: "done", status_category: "done", assignee_type: "agent", assignee_id: "planner",
  })) }),
}));
vi.mock("../../issues/components/status-icon", () => ({ StatusIcon: ({ status }: { status: string }) => <svg data-testid="status" data-status={status} /> }));
vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: ({ actorId }: { actorId: string }) => <span data-testid="assignee">{actorId}</span> }));

function mount() {
  return render(<I18nProvider locale="en" resources={{ en: { chat: enChat } }}><ChatPlanningIssues sessionId="chat" /></I18nProvider>);
}
afterEach(cleanup);

describe("chat created issues", () => {
  it("hides the entry with no related issues", () => {
    mocks.links = [];
    const { container } = mount();
    expect(container).toBeEmptyDOMElement();
  });

  it("starts collapsed and opens ordered shared issue chips with navigation", async () => {
    mocks.links = [{ id: "new", title: "Newest issue with a long title" }, { id: "old", title: "Historical completed issue" }];
    mount();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Tasks (2)" }));
    const links = await screen.findAllByRole("link");
    expect(links.map((link) => link.getAttribute("href"))).toEqual(["/issues/new", "/issues/old"]);
    expect(screen.getByText("QORA-10")).toBeInTheDocument();
    expect(screen.getByText("Newest issue with a long title")).toHaveClass("min-w-0", "truncate");
    expect(screen.getAllByTestId("status")[0]).toHaveAttribute("data-status", "done");
    expect(screen.getAllByTestId("assignee")).toHaveLength(2);
    expect(links[0]!.firstElementChild).toHaveClass("issue-mention");
    fireEvent.click(links[0]!);
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });
});
