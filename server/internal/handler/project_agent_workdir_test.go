package handler

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

type projectAgentWorkdirsTestResponse struct {
	Enabled  bool                             `json:"enabled"`
	Bindings []projectAgentWorkdirTestBinding `json:"bindings"`
}

type projectAgentWorkdirTestBinding struct {
	AgentID   string `json:"agent_id"`
	DaemonID  string `json:"daemon_id"`
	LocalPath string `json:"local_path"`
}

func projectAgentWorkdirRequest(userID, method, projectID, agentID string, body any) *http.Request {
	path := "/api/projects/" + projectID + "/agent-workdirs"
	if agentID == "" {
		return withURLParam(newRequestAs(userID, method, path, body), "id", projectID)
	}
	return withURLParams(newRequestAs(userID, method, path+"/"+agentID, body), "id", projectID, "agentId", agentID)
}

func TestProjectAgentWorkdirSettingsLifecycle(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	projectID := dbfx.Project(t, "Project with fixed workdirs", testutil.Cols{"creator_type": "member", "creator_id": testUserID})
	daemonID := "project-workdirs-" + uuid.NewString()
	runtimeID := dbfx.Runtime(t, "Workdir machine", testutil.Cols{"runtime_mode": "local", "daemon_id": daemonID, "status": "offline"})
	agentID := dbfx.Agent(t, "Workdir agent", runtimeID)
	otherAgentID := dbfx.Agent(t, "Second workdir agent", runtimeID)
	dbfx.Cleanup(t, `DELETE FROM project_agent_workdir WHERE workspace_id = $1 AND project_id = $2`, testWorkspaceID, projectID)

	get := func() projectAgentWorkdirsTestResponse {
		request := projectAgentWorkdirRequest(testUserID, "GET", projectID, "", nil)
		var result projectAgentWorkdirsTestResponse
		testutil.Call(t, testHandler.GetProjectAgentWorkdirSettings, request).Want(http.StatusOK).JSON(&result)
		return result
	}
	assertSettings := func(wantEnabled bool, wantBindings ...projectAgentWorkdirTestBinding) {
		t.Helper()
		result := get()
		if result.Enabled != wantEnabled || result.Bindings == nil || !slices.Equal(result.Bindings, wantBindings) {
			t.Fatalf("settings = %+v, want enabled=%v bindings=%+v", result, wantEnabled, wantBindings)
		}
	}
	putMode := func(enabled bool) {
		request := projectAgentWorkdirRequest(testUserID, "PUT", projectID, "", map[string]any{"enabled": enabled})
		testutil.Call(t, testHandler.UpdateProjectAgentWorkdirSettings, request).Want(http.StatusOK)
	}
	putBinding := func(agentID, path string) {
		request := projectAgentWorkdirRequest(testUserID, "PUT", projectID, agentID, map[string]any{
			"local_path":         path,
			"daemon_id":          "ignored-client-daemon",
			"expected_daemon_id": daemonID,
		})
		testutil.Call(t, testHandler.SetProjectAgentWorkdirBinding, request).Want(http.StatusOK)
	}

	assertSettings(false)
	first := projectAgentWorkdirTestBinding{AgentID: agentID, DaemonID: daemonID, LocalPath: "/srv/workdir"}
	second := projectAgentWorkdirTestBinding{AgentID: otherAgentID, DaemonID: daemonID, LocalPath: `C:\Work\project`}
	putBinding(agentID, " /srv/workdir ")
	assertSettings(false, first)
	putMode(true)
	assertSettings(true, first)
	putBinding(otherAgentID, second.LocalPath)
	putMode(false)
	bindings := []projectAgentWorkdirTestBinding{first, second}
	if bindings[0].AgentID > bindings[1].AgentID {
		bindings[0], bindings[1] = bindings[1], bindings[0]
	}
	assertSettings(false, bindings...)

	request := projectAgentWorkdirRequest(testUserID, "DELETE", projectID, agentID, nil)
	testutil.Call(t, testHandler.DeleteProjectAgentWorkdirBinding, request).Want(http.StatusOK)
	assertSettings(false, second)

	request = withURLParam(newRequest("DELETE", "/api/projects/"+projectID, nil), "id", projectID)
	testutil.Call(t, testHandler.DeleteProject, request).Want(http.StatusNoContent)
	var exists bool
	dbfx.QueryRow(t, `SELECT EXISTS (SELECT FROM project_agent_workdir WHERE workspace_id = $1 AND project_id = $2)`, testWorkspaceID, projectID).Scan(&exists)
	if exists {
		t.Fatal("setting row survived project deletion")
	}
}

func TestProjectAgentWorkdirWritesRequireOwnedLocalRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	projectID := dbfx.Project(t, "Project requiring runtime ownership")
	dbfx.Cleanup(t, `DELETE FROM project_agent_workdir WHERE workspace_id = $1 AND project_id = $2`, testWorkspaceID, projectID)
	otherUserID := createProjectPermissionTestMember(t, "member")
	ownedDaemonID := "owned-" + uuid.NewString()
	ownedID := dbfx.Runtime(t, "Owned workdir machine", testutil.Cols{
		"runtime_mode": "local", "daemon_id": ownedDaemonID,
	})
	localAgentID := dbfx.Agent(t, "Agent on owned machine", ownedID)
	cloudID := dbfx.Runtime(t, "Cloud workdir runtime", testutil.Cols{"daemon_id": "cloud-" + uuid.NewString()})
	cloudAgentID := dbfx.Agent(t, "Cloud workdir agent", cloudID)
	foreignWorkspaceID, foreignProjectID := foreignWorkspaceWithProject(t, "foreign-workdir-ws", "FWD")
	foreignAgentID := dbfx.Agent(t, "Foreign agent", "", testutil.Cols{"workspace_id": foreignWorkspaceID})
	foreignRuntimeID := dbfx.Runtime(t, "Foreign machine", testutil.Cols{
		"workspace_id": foreignWorkspaceID, "runtime_mode": "local", "daemon_id": "foreign-" + uuid.NewString(),
	})
	brokenAgentID := dbfx.Agent(t, "Agent pointing at foreign runtime", foreignRuntimeID)

	for _, test := range []struct {
		name             string
		userID           string
		projectID        string
		agentID          string
		path             string
		expectedDaemonID string
		want             int
	}{
		{"non-owner", otherUserID, projectID, localAgentID, "/srv/project", ownedDaemonID, http.StatusForbidden},
		{"relative path", testUserID, projectID, localAgentID, "relative/project", ownedDaemonID, http.StatusBadRequest},
		{"missing expected machine", testUserID, projectID, localAgentID, "/srv/project", "", http.StatusBadRequest},
		{"stale expected machine", testUserID, projectID, localAgentID, "/srv/project", "another-daemon", http.StatusConflict},
		{"cloud runtime", testUserID, projectID, cloudAgentID, "/srv/project", ownedDaemonID, http.StatusUnprocessableEntity},
		{"foreign agent", testUserID, projectID, foreignAgentID, "/srv/project", ownedDaemonID, http.StatusNotFound},
		{"foreign runtime", testUserID, projectID, brokenAgentID, "/srv/project", ownedDaemonID, http.StatusNotFound},
		{"foreign project", testUserID, foreignProjectID, localAgentID, "/srv/project", ownedDaemonID, http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := projectAgentWorkdirRequest(test.userID, "PUT", test.projectID, test.agentID, map[string]any{
				"local_path":         test.path,
				"expected_daemon_id": test.expectedDaemonID,
			})
			testutil.Call(t, testHandler.SetProjectAgentWorkdirBinding, request).Want(test.want)
		})
	}

	request := projectAgentWorkdirRequest(testUserID, "PUT", projectID, "", map[string]any{"enabled": true})
	request.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, testHandler.UpdateProjectAgentWorkdirSettings, request).Want(http.StatusForbidden)

	privateProjectID := dbfx.Project(t, "Hidden workdir project", testutil.Cols{"visibility": "private"})
	request = projectAgentWorkdirRequest(otherUserID, "PUT", privateProjectID, "", map[string]any{"enabled": true})
	testutil.Call(t, testHandler.UpdateProjectAgentWorkdirSettings, request).Want(http.StatusNotFound)

	request = projectAgentWorkdirRequest(testUserID, "GET", projectID, "", nil)
	var settings projectAgentWorkdirsTestResponse
	testutil.Call(t, testHandler.GetProjectAgentWorkdirSettings, request).Want(http.StatusOK).JSON(&settings)
	if settings.Enabled || len(settings.Bindings) != 0 {
		t.Fatalf("rejected writes left settings behind: %+v", settings)
	}
}
