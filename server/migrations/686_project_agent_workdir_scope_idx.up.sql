CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_project_agent_workdir_scope
    ON project_agent_workdir (workspace_id, project_id);
