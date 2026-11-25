package handler

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type projectAgentWorkdirBinding struct {
	AgentID   string `json:"agent_id,omitempty"`
	DaemonID  string `json:"daemon_id"`
	LocalPath string `json:"local_path"`
}

type projectAgentWorkdirSettings struct {
	Enabled  bool                         `json:"enabled"`
	Bindings []projectAgentWorkdirBinding `json:"bindings"`
}

func decodeProjectAgentWorkdirSettings(row db.ProjectAgentWorkdir) (projectAgentWorkdirSettings, error) {
	var bindings map[string]projectAgentWorkdirBinding
	if err := json.Unmarshal(row.Bindings, &bindings); err != nil {
		return projectAgentWorkdirSettings{}, err
	}
	settings := projectAgentWorkdirSettings{
		Enabled:  row.Enabled,
		Bindings: make([]projectAgentWorkdirBinding, 0, len(bindings)),
	}
	for _, agentID := range slices.Sorted(maps.Keys(bindings)) {
		binding := bindings[agentID]
		binding.AgentID = agentID
		settings.Bindings = append(settings.Bindings, binding)
	}
	return settings, nil
}

func (h *Handler) loadProjectAgentWorkdirSettings(ctx context.Context, workspaceID, projectID pgtype.UUID) (projectAgentWorkdirSettings, error) {
	row, err := h.Queries.GetProjectAgentWorkdir(ctx, db.GetProjectAgentWorkdirParams{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return projectAgentWorkdirSettings{Bindings: []projectAgentWorkdirBinding{}}, nil
	}
	if err != nil {
		return projectAgentWorkdirSettings{}, err
	}
	return decodeProjectAgentWorkdirSettings(row)
}

func (h *Handler) loadProjectForAgentWorkdir(w http.ResponseWriter, r *http.Request, write bool) (db.Project, db.Member, bool) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return db.Project{}, db.Member{}, false
	}
	project, ok := h.loadVisibleProject(w, r)
	if !ok {
		return db.Project{}, db.Member{}, false
	}
	if write {
		actorType, _ := h.resolveActor(r, requestUserID(r), workspaceID)
		if isMachineCredentialActor(r) || actorType == "agent" {
			writeError(w, http.StatusForbidden, "only human project members can change agent workdir settings")
			return db.Project{}, db.Member{}, false
		}
	}
	return project, member, true
}

func (h *Handler) GetProjectAgentWorkdirSettings(w http.ResponseWriter, r *http.Request) {
	project, _, ok := h.loadProjectForAgentWorkdir(w, r, false)
	if !ok {
		return
	}
	settings, err := h.loadProjectAgentWorkdirSettings(r.Context(), project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read agent workdir settings")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *Handler) UpdateProjectAgentWorkdirSettings(w http.ResponseWriter, r *http.Request) {
	project, _, ok := h.loadProjectForAgentWorkdir(w, r, true)
	if !ok {
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled must be a boolean")
		return
	}
	h.upsertProjectAgentWorkdirSettings(w, r, db.UpsertProjectAgentWorkdirParams{
		WorkspaceID: project.WorkspaceID,
		ProjectID:   project.ID,
		Enabled:     pgtype.Bool{Bool: *req.Enabled, Valid: true},
		Bindings:    []byte(`{}`),
	})
}

func (h *Handler) SetProjectAgentWorkdirBinding(w http.ResponseWriter, r *http.Request) {
	project, member, ok := h.loadProjectForAgentWorkdir(w, r, true)
	if !ok {
		return
	}
	var req struct {
		LocalPath        string `json:"local_path"`
		ExpectedDaemonID string `json:"expected_daemon_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	localPath := strings.TrimSpace(req.LocalPath)
	if !isAbsoluteLocalPath(localPath) || strings.ContainsRune(localPath, '\x00') {
		writeError(w, http.StatusBadRequest, "local_path must be an absolute path")
		return
	}
	if strings.TrimSpace(req.ExpectedDaemonID) == "" {
		writeError(w, http.StatusBadRequest, "expected_daemon_id is required")
		return
	}
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "agentId"))
	if !ok {
		return
	}
	if !agent.RuntimeID.Valid {
		writeError(w, http.StatusUnprocessableEntity, "agent must use a registered local runtime")
		return
	}
	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
		ID:          agent.RuntimeID,
		WorkspaceID: project.WorkspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read runtime")
		return
	}
	if runtime.RuntimeMode != "local" || !runtime.DaemonID.Valid || strings.TrimSpace(runtime.DaemonID.String) == "" || isQoderManagedDaemonID(runtime.DaemonID.String) {
		writeError(w, http.StatusUnprocessableEntity, "agent must use a registered local runtime")
		return
	}
	if !runtime.OwnerID.Valid || runtime.OwnerID != member.UserID {
		writeError(w, http.StatusForbidden, "only the runtime owner can set its local workdir")
		return
	}
	if runtime.DaemonID.String != req.ExpectedDaemonID {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "The agent's runtime machine changed. Refresh and select its workdir again.",
			"code":  "agent_workdir_machine_changed",
		})
		return
	}
	patch, err := json.Marshal(map[string]projectAgentWorkdirBinding{
		uuidToString(agent.ID): {DaemonID: runtime.DaemonID.String, LocalPath: localPath},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode agent workdir binding")
		return
	}
	h.upsertProjectAgentWorkdirSettings(w, r, db.UpsertProjectAgentWorkdirParams{
		WorkspaceID: project.WorkspaceID,
		ProjectID:   project.ID,
		Bindings:    patch,
	})
}

func (h *Handler) DeleteProjectAgentWorkdirBinding(w http.ResponseWriter, r *http.Request) {
	project, _, ok := h.loadProjectForAgentWorkdir(w, r, true)
	if !ok {
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "agentId"), "agent id")
	if !ok {
		return
	}
	row, err := h.Queries.RemoveProjectAgentWorkdirBinding(r.Context(), db.RemoveProjectAgentWorkdirBindingParams{
		WorkspaceID: project.WorkspaceID,
		ProjectID:   project.ID,
		AgentID:     uuidToString(agentID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, projectAgentWorkdirSettings{Bindings: []projectAgentWorkdirBinding{}})
		return
	}
	writeProjectAgentWorkdirSettingsResult(w, row, err)
}

func (h *Handler) upsertProjectAgentWorkdirSettings(w http.ResponseWriter, r *http.Request, params db.UpsertProjectAgentWorkdirParams) {
	row, err := h.Queries.UpsertProjectAgentWorkdir(r.Context(), params)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	writeProjectAgentWorkdirSettingsResult(w, row, err)
}

func writeProjectAgentWorkdirSettingsResult(w http.ResponseWriter, row db.ProjectAgentWorkdir, err error) {
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save agent workdir settings")
		return
	}
	settings, err := decodeProjectAgentWorkdirSettings(row)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read agent workdir settings")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}
