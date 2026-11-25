CREATE TABLE IF NOT EXISTS project_agent_workdir (
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    bindings JSONB NOT NULL DEFAULT '{}'::jsonb
);
