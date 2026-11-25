// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiError, setApiInstance, type ApiClient } from "@multica/core/api";
import type { AgentRuntime, ProjectAgentWorkdirs } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { ProjectAgentWorkdirsSection } from "./project-agent-workdirs";

vi.mock("@multica/core/auth", async (importOriginal) => {
  const auth = await importOriginal<typeof import("@multica/core/auth")>();
  const state = { user: { id: "me" } };
  return {
    ...auth,
    useAuthStore: Object.assign(
      (selector: (value: typeof state) => unknown) => selector(state),
      { getState: () => state },
    ),
  };
});

const currentMachine: AgentRuntime = {
  id: "remote-runtime",
  workspace_id: "ws",
  daemon_id: "remote-daemon",
  name: "Codex (Build server)",
  runtime_mode: "local",
  provider: "codex",
  launch_header: "",
  status: "offline",
  device_info: "Build server",
  metadata: {},
  owner_id: "me",
  visibility: "private",
  last_seen_at: null,
  created_at: "",
  updated_at: "",
};

const binding = { agentId: "build", daemonId: "remote-daemon", localPath: "/srv/project" };
let config: ProjectAgentWorkdirs;
let buildRuntime: AgentRuntime;
let queryClient: QueryClient;
const read = vi.fn(async () => config);
const toggle = vi.fn(async (_projectId: string, enabled: boolean) => config = { ...config, enabled });
const save = vi.fn(async (_projectId: string, agentId: string, localPath: string, expectedDaemonId: string) => config = {
  ...config,
  bindings: [{ agentId, localPath, daemonId: expectedDaemonId }],
});
const remove = vi.fn(async () => config = { ...config, bindings: [] });

function renderSection() {
  renderWithI18n(
    <QueryClientProvider client={queryClient}>
      <ProjectAgentWorkdirsSection wsId="ws" projectId="project" />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  config = { enabled: false, bindings: [] };
  buildRuntime = currentMachine;
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  setApiInstance({
    getProjectAgentWorkdirs: read,
    setProjectAgentWorkdirsEnabled: toggle,
    setProjectAgentWorkdir: save,
    deleteProjectAgentWorkdir: remove,
    listAgents: async () => [
      { id: "build", name: "Build Agent", runtime_id: buildRuntime.id },
      { id: "review", name: "Review Agent", runtime_id: "shared-runtime" },
    ],
    listRuntimes: async () => [
      buildRuntime,
      { ...currentMachine, id: "shared-runtime", daemon_id: "shared-daemon", owner_id: "other" },
    ],
  } as unknown as ApiClient);
});

afterEach(() => queryClient.clear());

describe("project execution directories", () => {
  it("shows the new mode only after the server confirms the change", async () => {
    let finish!: (value: ProjectAgentWorkdirs) => void;
    toggle.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    renderSection();

    const control = await screen.findByRole("switch", { name: "Fixed directory per agent" });
    expect(control).not.toBeChecked();
    fireEvent.click(control);
    await waitFor(() => expect(toggle).toHaveBeenCalledWith("project", true));
    expect(control).toHaveAttribute("aria-disabled", "true");
    expect(control).not.toBeChecked();
    await act(async () => finish(config = { enabled: true, bindings: [] }));
    await waitFor(() => expect(control).toBeChecked());
  });

  it("adds a manually entered path for an agent on an owned headless machine", async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole("button", { name: "Add directory" }));
    const dialog = within(await screen.findByRole("dialog", { name: "Add directory" }));
    await user.click(dialog.getByLabelText("Agent"));
    expect(screen.queryByRole("option", { name: "Review Agent" })).not.toBeInTheDocument();
    await user.click(await screen.findByRole("option", { name: "Build Agent" }));
    expect(dialog.getByText("Build server")).toBeVisible();

    const path = dialog.getByLabelText("Absolute path");
    fireEvent.change(path, { target: { value: "relative/project" } });
    await user.click(dialog.getByRole("button", { name: "Save" }));
    expect(dialog.getByRole("alert")).toHaveTextContent("Enter an absolute path");
    expect(save).not.toHaveBeenCalled();

    fireEvent.change(path, { target: { value: "/srv/project" } });
    await user.click(dialog.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(save).toHaveBeenCalledWith("project", "build", "/srv/project", "remote-daemon"));
    expect(await screen.findByText("/srv/project")).toBeVisible();
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add directory" })).not.toBeInTheDocument());
  });

  it("keeps the draft on a failed edit and waits for confirmation before removing the directory", async () => {
    const user = userEvent.setup();
    config = { enabled: true, bindings: [binding] };
    save.mockRejectedValueOnce(new ApiError("denied", 403, "Forbidden"));
    renderSection();

    await user.click(await screen.findByRole("button", { name: "Edit directory for Build Agent" }));
    const dialog = within(await screen.findByRole("dialog"));
    const path = dialog.getByLabelText("Absolute path");
    fireEvent.change(path, { target: { value: "/srv/new-project" } });
    await user.click(dialog.getByRole("button", { name: "Save" }));
    expect(await dialog.findByRole("alert")).toHaveTextContent("Only the owner");
    expect(path).toHaveValue("/srv/new-project");
    await user.click(dialog.getByRole("button", { name: "Cancel" }));

    await user.click(screen.getByRole("button", { name: "Remove directory for Build Agent" }));
    const confirmation = within(await screen.findByRole("alertdialog"));
    expect(remove).not.toHaveBeenCalled();
    await user.click(confirmation.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(remove).toHaveBeenCalledWith("project", "build"));
    await waitFor(() => expect(screen.queryByText("/srv/project")).not.toBeInTheDocument());
  });

  it("keeps the draft on the displayed machine when the agent moves during an edit", async () => {
    const user = userEvent.setup();
    config = { enabled: true, bindings: [binding] };
    save.mockImplementationOnce(async () => {
      buildRuntime = {
        ...currentMachine,
        id: "new-runtime",
        daemon_id: "new-daemon",
        name: "Codex (New server)",
        device_info: "New server",
      };
      throw new ApiError("machine changed", 409, "Conflict");
    });
    renderSection();

    await user.click(await screen.findByRole("button", { name: "Edit directory for Build Agent" }));
    const dialog = within(await screen.findByRole("dialog"));
    const path = dialog.getByLabelText("Absolute path");
    fireEvent.change(path, { target: { value: "/srv/my-edit" } });
    await user.click(dialog.getByRole("button", { name: "Save" }));

    expect(await dialog.findByRole("alert")).toHaveTextContent("This agent changed machines");
    expect(path).toHaveValue("/srv/my-edit");
    expect(dialog.getByText("Build server")).toBeVisible();
    expect(dialog.getByRole("button", { name: "Save" })).toBeDisabled();
    await user.click(dialog.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    await user.click(await screen.findByRole("button", { name: "Edit directory for Build Agent" }));
    const reopened = within(await screen.findByRole("dialog"));
    expect(reopened.getByText("New server")).toBeVisible();
    expect(reopened.getByLabelText("Absolute path")).toHaveValue("");
  });

  it("leaves the legacy Resources view usable with an older server", async () => {
    read.mockRejectedValueOnce(new ApiError("not found", 404, "Not Found"));
    renderSection();

    await waitFor(() => expect(queryClient.getQueryState([
      "projects", "ws", "detail", "project", "agent-workdirs",
    ])?.status).toBe("error"));
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    expect(screen.queryByText("Execution directories")).not.toBeInTheDocument();
  });
});
