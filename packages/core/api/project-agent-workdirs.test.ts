// @vitest-environment node

import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

const operations = [
  { method: "GET", suffix: "", call: (client: ApiClient) => client.getProjectAgentWorkdirs("project") },
  { method: "PUT", suffix: "", body: { enabled: true }, call: (client: ApiClient) => client.setProjectAgentWorkdirsEnabled("project", true) },
  { method: "PUT", suffix: "/agent", body: { local_path: "/srv/repo", expected_daemon_id: "daemon" }, call: (client: ApiClient) => client.setProjectAgentWorkdir("project", "agent", "/srv/repo", "daemon") },
  { method: "DELETE", suffix: "/agent", call: (client: ApiClient) => client.deleteProjectAgentWorkdir("project", "agent") },
];

function respondWith(body: unknown) {
  const fetch = vi.fn().mockImplementation(async () => Response.json(body));
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

afterEach(() => vi.unstubAllGlobals());

describe("project agent workdir API", () => {
  it.each(operations)("uses $method /agent-workdirs$suffix and decodes the response", async ({ method, suffix, body, call }) => {
    const fetch = respondWith({
      enabled: true,
      bindings: [{ agent_id: "agent", daemon_id: "daemon", local_path: "/srv/repo" }],
    });
    const client = new ApiClient("https://api.example.test");

    await expect(call(client)).resolves.toEqual({
      enabled: true,
      bindings: [{ agentId: "agent", daemonId: "daemon", localPath: "/srv/repo" }],
    });
    expect(fetch).toHaveBeenCalledWith(
      `https://api.example.test/api/projects/project/agent-workdirs${suffix}`,
      expect.objectContaining({ method, body: body && JSON.stringify(body) }),
    );
  });

  it.each(operations)("returns an unavailable result for a malformed $method response", async ({ call }) => {
    respondWith({ enabled: true, bindings: [{ agent_id: "agent", local_path: 42 }] });
    const client = new ApiClient("https://api.example.test");

    await expect(call(client)).resolves.toBeNull();
  });
});
