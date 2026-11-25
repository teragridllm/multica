package handler

import (
	"encoding/json"
	"path"
	"slices"
	"strings"
)

// The daemon only needs the selected directory, in its existing resource format.
// Filtering the older local resources here keeps the assignment unambiguous.
func routeProjectAgentWorkdir(resp *AgentTaskResponse, settings projectAgentWorkdirSettings, agentID, daemonID string, supportsLocalDirectory bool) (path, rejection string) {
	if !settings.Enabled {
		return "", ""
	}
	if daemonID == "" {
		return "", "Select a local execution machine for this agent before running it in this project."
	}
	for _, binding := range settings.Bindings {
		if binding.AgentID != agentID || binding.LocalPath == "" {
			continue
		}
		if binding.DaemonID != daemonID {
			return "", "Set this agent's project execution directory for its current execution machine."
		}
		if !supportsLocalDirectory {
			return "", "Update Multica on this agent's execution machine to use its project execution directory."
		}

		ref, _ := json.Marshal(localDirectoryRef{
			DaemonID: daemonID, LocalPath: binding.LocalPath, ExecutionMode: localDirectoryModeInPlace,
		})
		resp.ProjectResources = slices.DeleteFunc(resp.ProjectResources, func(resource ProjectResourceData) bool {
			return resource.ResourceType == "local_directory"
		})
		resp.ProjectResources = append(resp.ProjectResources, ProjectResourceData{
			ID: agentID, ResourceType: "local_directory", ResourceRef: ref,
		})
		return binding.LocalPath, ""
	}
	return "", "Set an execution directory for this agent in the project's resources before running it again."
}

func clearProjectAgentWorkdirResume(resp *AgentTaskResponse, path string) bool {
	if path == "" || sameProjectAgentWorkdir(path, resp.PriorWorkDir) {
		return false
	}
	if resp.PriorSessionID != "" {
		resp.PriorSessionResumeUnavailable = true
	}
	resp.PriorSessionID = ""
	resp.PriorWorkDir = ""
	return true
}

// The daemon reports a cleaned path in its own OS format. Compare with that
// format even when the server and daemon are on different operating systems.
func sameProjectAgentWorkdir(configured, reported string) bool {
	if len(reported) >= 3 && isDriveLetter(reported[0]) && reported[1] == ':' {
		if len(configured) < 3 || !strings.EqualFold(configured[:2], reported[:2]) {
			return false
		}
		return path.Clean(strings.ReplaceAll(configured[2:], "\\", "/")) ==
			path.Clean(strings.ReplaceAll(reported[2:], "\\", "/"))
	}
	if strings.HasPrefix(reported, `\\`) {
		if !strings.HasPrefix(configured, `\\`) && !strings.HasPrefix(configured, "//") {
			return false
		}
		configured = strings.ReplaceAll(configured, "\\", "/")
		reported = strings.ReplaceAll(reported, "\\", "/")
	}
	return path.Clean(configured) == path.Clean(reported)
}
