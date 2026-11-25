import { onlineManager, queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import { runtimeKeys } from "../runtimes/queries";
import { workspaceKeys } from "../workspace/queries";
import { projectKeys } from "./queries";

export function isProjectAgentWorkdirsUnsupported(error: unknown) {
  return error instanceof ApiError && (error.status === 404 || error.status === 405);
}

export function projectAgentWorkdirsOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: [...projectKeys.detail(wsId, projectId), "agent-workdirs"] as const,
    queryFn: () => api.getProjectAgentWorkdirs(projectId),
    staleTime: 0,
    retry: (count, error) => count < 2 && !isProjectAgentWorkdirsUnsupported(error),
  });
}

type AgentWorkdirsChange =
  | { type: "toggle"; enabled: boolean }
  | { type: "save"; agentId: string; localPath: string; expectedDaemonId: string }
  | { type: "delete"; agentId: string };

export function useUpdateProjectAgentWorkdirs(wsId: string, projectId: string) {
  const qc = useQueryClient();
  const { queryKey } = projectAgentWorkdirsOptions(wsId, projectId);
  return useMutation({
    networkMode: "always",
    retry: false,
    mutationFn: async (change: AgentWorkdirsChange) => {
      if (!onlineManager.isOnline()) throw new Error("Offline");
      const config = await (change.type === "toggle"
        ? api.setProjectAgentWorkdirsEnabled(projectId, change.enabled)
        : change.type === "save"
          ? api.setProjectAgentWorkdir(projectId, change.agentId, change.localPath, change.expectedDaemonId)
          : api.deleteProjectAgentWorkdir(projectId, change.agentId));
      if (!config) throw new Error("Malformed project execution directories response");
      return config;
    },
    onSuccess: async (config) => {
      await qc.cancelQueries({ queryKey });
      qc.setQueryData(queryKey, config);
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey });
    },
    onError: async (error, change) => {
      if (change.type === "save" && error instanceof ApiError && error.status === 409) {
        await Promise.all([
          qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) }),
          qc.invalidateQueries({ queryKey: runtimeKeys.list(wsId) }),
        ]);
      }
    },
  });
}
