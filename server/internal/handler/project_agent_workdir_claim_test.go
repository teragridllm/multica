package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRouteProjectAgentWorkdirSelectsOnlyTheClaimingAgentsDirectory(t *testing.T) {
	settings := projectAgentWorkdirSettings{
		Enabled: true,
		Bindings: []projectAgentWorkdirBinding{
			{AgentID: "developer", DaemonID: "machine-a", LocalPath: "/home/runner/develop"},
			{AgentID: "reviewer", DaemonID: "machine-a", LocalPath: "/home/runner/review"},
		},
	}
	for _, tc := range []struct{ agentID, path string }{
		{"developer", "/home/runner/develop"},
		{"reviewer", "/home/runner/review"},
	} {
		t.Run(tc.agentID, func(t *testing.T) {
			resp := AgentTaskResponse{ProjectResources: []ProjectResourceData{
				{ResourceType: "github_repo", ResourceRef: json.RawMessage(`{"url":"https://example.com/team/repo"}`)},
				{ResourceType: "local_directory", ResourceRef: json.RawMessage(`{"daemon_id":"machine-a","local_path":"/home/runner/shared","execution_mode":"worktree"}`)},
			}}
			path, reason := routeProjectAgentWorkdir(&resp, settings, tc.agentID, "machine-a", true)
			if path != tc.path || reason != "" {
				t.Fatalf("claim selected (%q, %q), want (%q, no error)", path, reason, tc.path)
			}
			if len(resp.ProjectResources) != 2 || resp.ProjectResources[0].ResourceType != "github_repo" || resp.ProjectResources[1].ResourceType != "local_directory" {
				t.Fatalf("claim should carry the repository and one local directory: %+v", resp.ProjectResources)
			}
			var ref localDirectoryRef
			if err := json.Unmarshal(resp.ProjectResources[1].ResourceRef, &ref); err != nil {
				t.Fatal(err)
			}
			if ref.LocalPath != tc.path || ref.DaemonID != "machine-a" || ref.ExecutionMode != "in_place" {
				t.Fatalf("claim local_directory = %+v", ref)
			}
		})
	}
}

func TestRouteProjectAgentWorkdirRejectsUnusableBindings(t *testing.T) {
	settings := projectAgentWorkdirSettings{
		Enabled:  true,
		Bindings: []projectAgentWorkdirBinding{{AgentID: "developer", DaemonID: "machine-a", LocalPath: "/home/runner/develop"}},
	}
	for _, tc := range []struct {
		name, agent, daemon, want string
		supported                 bool
	}{
		{"no binding", "reviewer", "machine-a", "Set an execution directory", true},
		{"wrong machine", "developer", "machine-b", "current execution machine", true},
		{"no local machine", "developer", "", "local execution machine", true},
		{"old daemon", "developer", "machine-a", "Update Multica", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := AgentTaskResponse{}
			path, reason := routeProjectAgentWorkdir(&resp, settings, tc.agent, tc.daemon, tc.supported)
			if path != "" || !strings.Contains(reason, tc.want) || len(resp.ProjectResources) != 0 {
				t.Fatalf("claim selected (%q, %q), resources %+v; want rejection mentioning %q", path, reason, resp.ProjectResources, tc.want)
			}
		})
	}
}

func TestRouteProjectAgentWorkdirLeavesProjectResourcesWhenDisabled(t *testing.T) {
	resp := AgentTaskResponse{ProjectResources: []ProjectResourceData{{ID: "shared", ResourceType: "local_directory"}}}
	path, reason := routeProjectAgentWorkdir(&resp, projectAgentWorkdirSettings{}, "developer", "machine-a", false)
	if path != "" || reason != "" || len(resp.ProjectResources) != 1 || resp.ProjectResources[0].ID != "shared" {
		t.Fatalf("disabled mode changed ordinary claim: path=%q reason=%q resources=%+v", path, reason, resp.ProjectResources)
	}
}

func TestClearProjectAgentWorkdirResumeOnPathChange(t *testing.T) {
	for _, tc := range []struct {
		name, path, priorPath string
		cleared               bool
	}{
		{"fixed mode off", "", "/home/runner/old", false},
		{"same directory", "/home/runner/new", "/home/runner/new", false},
		{"directory with trailing slash", "/home/runner/new/", "/home/runner/new", false},
		{"directory with dot component", "/home/runner/./new", "/home/runner/new", false},
		{"Windows drive and separators", "c:/Work/project/", "C:\\Work\\project", false},
		{"Windows UNC separators", "//server/share/project/", "\\\\server\\share\\project", false},
		{"changed directory", "/home/runner/new", "/home/runner/old", true},
		{"distinct Windows share", "//server/share-a/project", "\\\\server\\share-b\\project", true},
		{"unknown prior directory", "/home/runner/new", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := AgentTaskResponse{PriorSessionID: "old-session", PriorWorkDir: tc.priorPath}
			if cleared := clearProjectAgentWorkdirResume(&resp, tc.path); cleared != tc.cleared {
				t.Fatalf("cleared = %v, want %v", cleared, tc.cleared)
			}
			if tc.cleared {
				if resp.PriorSessionID != "" || resp.PriorWorkDir != "" || !resp.PriorSessionResumeUnavailable {
					t.Fatalf("changed path retained resume metadata: %+v", resp)
				}
			} else if resp.PriorSessionID != "old-session" || resp.PriorWorkDir != tc.priorPath || resp.PriorSessionResumeUnavailable {
				t.Fatalf("unchanged path lost resume metadata: %+v", resp)
			}
		})
	}
}

func TestIssueClaimUsesCurrentProjectAgentWorkdir(t *testing.T) {
	const daemonID = "project-agent-workdir-claim-test"
	project := dbfx.Project(t, "Fixed directory claim")
	type fixture struct {
		name, provider, path string
		runtimeID, taskID    string
		mentioned            bool
	}
	agents := []fixture{
		{name: "Developer", provider: "codex", path: "/home/runner/develop"},
		{name: "Mentioned reviewer", provider: "opencode", path: "/home/runner/review", mentioned: true},
		{name: "Unconfigured", provider: "claude"},
	}
	bindings := make(map[string]map[string]string)
	for i := range agents {
		agent := &agents[i]
		agent.runtimeID = dbfx.Runtime(t, "Fixed directory "+agent.name, testutil.Cols{
			"daemon_id": daemonID, "runtime_mode": "local", "provider": agent.provider,
		})
		agentID := dbfx.Agent(t, "Fixed directory "+agent.name, agent.runtimeID, testutil.Cols{"runtime_mode": "local"})
		issueFields := testutil.Cols{
			"project_id": project, "assignee_type": "agent", "assignee_id": agentID,
		}
		if agent.mentioned {
			issueFields["assignee_type"] = "member"
			issueFields["assignee_id"] = testUserID
		}
		issueID := dbfx.Issue(t, "Fixed directory issue", issueFields)
		taskFields := testutil.Cols{"issue_id": issueID, "runtime_id": agent.runtimeID}
		if agent.mentioned {
			taskFields["trigger_comment_id"] = dbfx.Comment(t, issueID, "@Mentioned reviewer please review")
		}
		agent.taskID = dbfx.Task(t, agentID, taskFields)
		if agent.path != "" {
			bindings[agentID] = map[string]string{"daemon_id": daemonID, "local_path": agent.path}
		}
	}
	data, err := json.Marshal(bindings)
	if err != nil {
		t.Fatal(err)
	}
	// The settings are saved after the tasks enter the queue.
	dbfx.Exec(t, `INSERT INTO project_agent_workdir (workspace_id, project_id, enabled, bindings)
		VALUES ($1, $2, true, $3)`, testWorkspaceID, project, data)
	t.Cleanup(func() {
		dbfx.Exec(t, `DELETE FROM project_agent_workdir WHERE workspace_id = $1 AND project_id = $2`, testWorkspaceID, project)
	})

	for _, tc := range agents {
		t.Run(tc.name, func(t *testing.T) {
			req := newOwnerDaemonRequest("POST", "/api/daemon/runtimes/"+tc.runtimeID+"/claim", nil)
			req = withURLParam(req, "runtimeId", tc.runtimeID)
			req.Header.Set("X-Client-Capabilities", protocol.DaemonCapabilityLocalWorktreeV1)
			var resp struct{ Task *AgentTaskResponse }
			testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK).JSON(&resp)
			if tc.path == "" {
				if resp.Task != nil {
					t.Fatalf("an unconfigured agent received task %s", resp.Task.ID)
				}
				var status, failure, message string
				dbfx.QueryRow(t, `SELECT status, failure_reason, error FROM agent_task_queue WHERE id = $1`, tc.taskID).
					Scan(&status, &failure, &message)
				if status != "failed" || failure != "local_directory_error" || !strings.Contains(message, "Set an execution directory") {
					t.Fatalf("run settled as (%q, %q, %q)", status, failure, message)
				}
				return
			}
			if resp.Task == nil || resp.Task.ID != tc.taskID || len(resp.Task.ProjectResources) != 1 {
				t.Fatalf("expected run %s with one local resource, got %+v", tc.taskID, resp.Task)
			}
			resource := resp.Task.ProjectResources[0]
			var ref localDirectoryRef
			if err := json.Unmarshal(resource.ResourceRef, &ref); err != nil {
				t.Fatal(err)
			}
			if resource.ResourceType != "local_directory" || ref.LocalPath != tc.path || ref.DaemonID != daemonID {
				t.Fatalf("claim workdir = %+v, want %q on %q", ref, tc.path, daemonID)
			}
		})
	}
}
