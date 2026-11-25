-- name: GetProjectAgentWorkdir :one
SELECT * FROM project_agent_workdir
WHERE workspace_id = $1 AND project_id = $2;

-- name: UpsertProjectAgentWorkdir :one
-- Take the workspace and project locks in deletion order so neither teardown
-- can sweep the settings and then let a concurrent save recreate them.
WITH locked_workspace AS MATERIALIZED (
    SELECT id FROM workspace
    WHERE id = sqlc.arg(workspace_id)::uuid
    FOR KEY SHARE
), locked_project AS MATERIALIZED (
    SELECT project.workspace_id, project.id
    FROM locked_workspace
    JOIN project ON project.workspace_id = locked_workspace.id
    WHERE project.id = sqlc.arg(project_id)::uuid
    FOR KEY SHARE OF project
)
INSERT INTO project_agent_workdir AS settings (workspace_id, project_id, enabled, bindings)
SELECT workspace_id, id, COALESCE(sqlc.narg(enabled)::boolean, FALSE), sqlc.arg(bindings)::jsonb
FROM locked_project
ON CONFLICT (workspace_id, project_id) DO UPDATE
SET enabled = COALESCE(sqlc.narg(enabled)::boolean, settings.enabled),
    bindings = settings.bindings || EXCLUDED.bindings
RETURNING *;

-- name: RemoveProjectAgentWorkdirBinding :one
UPDATE project_agent_workdir
SET bindings = bindings - sqlc.arg(agent_id)::text
WHERE workspace_id = sqlc.arg(workspace_id)::uuid AND project_id = sqlc.arg(project_id)::uuid
RETURNING *;

-- name: DeleteProjectAgentWorkdir :exec
DELETE FROM project_agent_workdir
WHERE workspace_id = $1 AND project_id = $2;

-- name: DeleteWorkspaceProjectAgentWorkdirs :exec
DELETE FROM project_agent_workdir WHERE workspace_id = $1;
