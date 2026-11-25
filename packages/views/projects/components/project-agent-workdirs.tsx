"use client";

import { useId, useMemo, useState, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import {
  isProjectAgentWorkdirsUnsupported,
  projectAgentWorkdirsOptions,
  useUpdateProjectAgentWorkdirs,
} from "@multica/core/projects";
import { runtimeListOptions } from "@multica/core/runtimes";
import { agentListOptions } from "@multica/core/workspace/queries";
import { IconButton } from "@multica/ui/components/common/icon-button";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Spinner } from "@multica/ui/components/ui/spinner";
import { Switch } from "@multica/ui/components/ui/switch";
import { FolderOpen, Pencil, Plus, Trash2 } from "@multica/ui/icons";
import { useT } from "../../i18n";
import { buildRuntimeMachines } from "../../runtimes/components/runtime-machines";

type WorkdirAgent = { id: string; name: string; machine: string; daemonId: string };
type SaveWorkdir = (agent: WorkdirAgent, localPath: string) => Promise<unknown>;

export function ProjectAgentWorkdirsSection({ wsId, projectId }: { wsId: string; projectId: string }) {
  const { t } = useT("projects");
  const userId = useAuthStore((state) => state.user?.id);
  const config = useQuery(projectAgentWorkdirsOptions(wsId, projectId));
  const agents = useQuery({ ...agentListOptions(wsId), enabled: !!config.data });
  const runtimes = useQuery({ ...runtimeListOptions(wsId), enabled: !!config.data });
  const update = useUpdateProjectAgentWorkdirs(wsId, projectId);
  const [toggleError, setToggleError] = useState(false);
  const switchLabelId = useId();
  const behaviorId = useId();

  const machines = useMemo(() => new Map(
    buildRuntimeMachines(runtimes.data ?? [], { now: Date.now() })
      .filter((machine) => machine.daemonId)
      .map((machine) => [machine.daemonId, machine.title]),
  ), [runtimes.data]);

  if (isProjectAgentWorkdirsUnsupported(config.error)) {
    return null;
  }

  const data = config.data;
  if (!data) {
    return (
      <div className="space-y-1.5">
        <h3 className="flex h-6 items-center text-micro font-medium text-muted-foreground">
          {t(($) => $.agent_workdirs.title)}
        </h3>
        {config.isPending ? <Spinner className="size-3" /> : (
          <p role="alert" className="text-caption text-destructive">
            {t(($) => $.agent_workdirs.load_error)}
          </p>
        )}
      </div>
    );
  }

  const agentsById = new Map(agents.data?.map((agent) => [agent.id, agent]));
  const runtimesById = new Map(runtimes.data?.map((runtime) => [runtime.id, runtime]));
  const boundAgentIds = new Set(data.bindings.map((binding) => binding.agentId));
  const unknownMachine = t(($) => $.agent_workdirs.unknown_machine);
  const ownedAgents = new Map<string, WorkdirAgent>();
  for (const agent of agentsById.values()) {
    const runtime = runtimesById.get(agent.runtime_id);
    if (!userId || agent.archived_at || runtime?.owner_id !== userId ||
        runtime.runtime_mode !== "local" || !runtime.daemon_id ||
        runtime.daemon_id.startsWith("qoder-managed-")) {
      continue;
    }
    ownedAgents.set(agent.id, {
      id: agent.id,
      name: agent.name,
      machine: machines.get(runtime.daemon_id) ?? unknownMachine,
      daemonId: runtime.daemon_id,
    });
  }
  const availableAgents = [...ownedAgents.values()].filter((agent) => !boundAgentIds.has(agent.id));
  const toggle = async (enabled: boolean) => {
    setToggleError(false);
    try {
      await update.mutateAsync({ type: "toggle", enabled });
    } catch {
      setToggleError(true);
    }
  };
  const save: SaveWorkdir = (agent, localPath) => update.mutateAsync({
    type: "save", agentId: agent.id, localPath, expectedDaemonId: agent.daemonId,
  });

  return (
    <div className="space-y-1.5">
      <div className="flex h-6 items-center justify-between gap-1">
        <h3 className="truncate text-micro font-medium text-muted-foreground">{t(($) => $.agent_workdirs.title)}</h3>
        <WorkdirFormDialog agents={availableAgents} busy={update.isPending} onSave={save} />
      </div>

      <div className="flex items-center gap-2">
        <span id={switchLabelId} className="min-w-0 flex-1 text-caption">{t(($) => $.agent_workdirs.enabled)}</span>
        {update.isPending && update.variables?.type === "toggle" && <Spinner className="size-3" aria-hidden="true" />}
        <Switch
          size="sm"
          aria-labelledby={switchLabelId}
          aria-describedby={behaviorId}
          aria-busy={update.isPending && update.variables?.type === "toggle"}
          checked={data.enabled}
          disabled={!userId || update.isPending}
          onCheckedChange={(enabled) => void toggle(enabled)}
        />
      </div>
      <p id={behaviorId} className="text-micro text-muted-foreground">{t(($) => $.agent_workdirs.behavior)}</p>
      {toggleError && <p role="alert" className="text-caption text-destructive">{t(($) => $.agent_workdirs.save_setting_error)}</p>}

      {data.bindings.map((binding) => {
        const agent = agentsById.get(binding.agentId);
        const ownedAgent = ownedAgents.get(binding.agentId);
        const runtime = agent && runtimesById.get(agent.runtime_id);
        const moved = runtime && runtime.daemon_id !== binding.daemonId;
        const agentName = agent?.name ?? t(($) => $.agent_workdirs.unknown_agent);
        return (
          <div key={binding.agentId} className="flex items-start gap-2 text-caption">
            <FolderOpen className="mt-1 size-3.5 shrink-0 text-muted-foreground" />
            <div className="min-w-0 flex-1 py-0.5">
              <div className="truncate font-medium" title={agentName}>{agentName}</div>
              <div className="truncate text-micro text-muted-foreground">{machines.get(binding.daemonId) ?? unknownMachine}</div>
              <div className="truncate font-mono text-micro text-muted-foreground" title={binding.localPath}>{binding.localPath}</div>
              {moved && <p className="mt-1 text-micro text-muted-foreground">{t(($) => $.agent_workdirs.moved)}</p>}
            </div>
            <div className="flex shrink-0 items-center">
              <WorkdirFormDialog
                agents={ownedAgent ? [ownedAgent] : []}
                editing={{ agentName, initialPath: moved ? "" : binding.localPath }}
                busy={update.isPending}
                onSave={save}
              />
              {userId && (
                <RemoveWorkdirDialog
                  agentName={agentName}
                  busy={update.isPending}
                  onRemove={() => update.mutateAsync({ type: "delete", agentId: binding.agentId })}
                />
              )}
            </div>
          </div>
        );
      })}
      {data.bindings.length === 0 && <p className="text-caption text-muted-foreground">{t(($) => $.agent_workdirs.empty)}</p>}
      {(agents.isError || runtimes.isError) && <p role="alert" className="text-caption text-destructive">{t(($) => $.agent_workdirs.agent_load_error)}</p>}
      {userId && agents.data && runtimes.data && ownedAgents.size === 0 && (
        <p className="text-micro text-muted-foreground">{t(($) => $.agent_workdirs.owner_hint)}</p>
      )}
    </div>
  );
}

function WorkdirFormDialog({
  agents,
  editing,
  busy,
  onSave,
}: {
  agents: WorkdirAgent[];
  editing?: { agentName: string; initialPath: string };
  busy: boolean;
  onSave: SaveWorkdir;
}) {
  const { t } = useT("projects");
  const { t: common } = useT("common");
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState<WorkdirAgent>();
  const [path, setPath] = useState("");
  const [error, setError] = useState<"path_invalid" | "machine_owner_error" | "runtime_required_error" | "machine_changed_error" | "save_directory_error" | null>(null);
  const agentLabelId = useId();
  const pathId = useId();
  const pathErrorId = useId();
  const title = editing
    ? t(($) => $.agent_workdirs.edit, { agent: editing.agentName })
    : t(($) => $.agent_workdirs.add);

  const onOpenChange = (next: boolean) => {
    if (busy) return;
    if (next) {
      setSelected(editing ? agents[0] : undefined);
      setPath(editing?.initialPath ?? "");
      setError(null);
    }
    setOpen(next);
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!selected || busy || error === "machine_changed_error") return;
    const value = path.trim();
    if (!/^(?:\/|\\\\|[A-Za-z]:[\\/])/.test(value) || value.includes("\u0000")) {
      setError("path_invalid");
      return;
    }
    setError(null);
    try {
      await onSave(selected, value);
      setOpen(false);
    } catch (err) {
      const status = err instanceof ApiError ? err.status : null;
      if (status === 400) setError("path_invalid");
      else if (status === 403) setError("machine_owner_error");
      else if (status === 409) setError("machine_changed_error");
      else if (status === 422) setError("runtime_required_error");
      else setError("save_directory_error");
    }
  };

  if (!open && !agents.length) return null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger render={
        <IconButton size="icon-xs" label={title} disabled={busy || !agents.length}>
          {editing ? <Pencil /> : <Plus />}
        </IconButton>
      } />
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{t(($) => $.agent_workdirs.path_hint)}</DialogDescription>
        </DialogHeader>
        <form className="space-y-4" onSubmit={(event) => void submit(event)}>
          {!editing && (
            <div className="space-y-1.5">
              <span id={agentLabelId} className="text-caption font-medium">{t(($) => $.agent_workdirs.agent)}</span>
              <Select
                items={agents.map(({ id, name }) => ({ value: id, label: name }))}
                value={selected?.id ?? null}
                onValueChange={(id) => {
                  setSelected(agents.find((agent) => agent.id === id));
                  setPath("");
                  setError(null);
                }}
                disabled={busy}
              >
                <SelectTrigger aria-labelledby={agentLabelId} className="w-full">
                  <SelectValue placeholder={t(($) => $.agent_workdirs.choose_agent)} />
                </SelectTrigger>
                <SelectContent>
                  {agents.map((agent) => <SelectItem key={agent.id} value={agent.id}>{agent.name}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
          )}
          {selected && (
            <dl className="space-y-1 text-caption">
              <dt className="font-medium">{t(($) => $.agent_workdirs.machine)}</dt>
              <dd className="wrap-anywhere text-muted-foreground">{selected.machine}</dd>
            </dl>
          )}
          <div className="space-y-1.5">
            <label htmlFor={pathId} className="text-caption font-medium">{t(($) => $.agent_workdirs.path)}</label>
            <Input
              id={pathId}
              value={path}
              disabled={!selected || busy}
              autoComplete="off"
              autoCapitalize="off"
              spellCheck={false}
              // eslint-disable-next-line no-restricted-syntax -- A literal example filesystem path.
              placeholder="/srv/project"
              aria-invalid={error === "path_invalid" || undefined}
              aria-describedby={error ? pathErrorId : undefined}
              onChange={(event) => {
                setPath(event.target.value);
                if (error !== "machine_changed_error") setError(null);
              }}
            />
            {error && <p id={pathErrorId} role="alert" className="text-caption text-destructive">{t(($) => $.agent_workdirs[error])}</p>}
          </div>
          <DialogFooter>
            <DialogClose render={<Button type="button" variant="outline" disabled={busy} />}>{common(($) => $.cancel)}</DialogClose>
            <Button type="submit" disabled={!selected || !path.trim() || busy || error === "machine_changed_error"} aria-busy={busy}>
              {busy && <Spinner className="size-3.5" aria-hidden="true" />}
              {common(($) => $.save)}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RemoveWorkdirDialog({
  agentName,
  busy,
  onRemove,
}: {
  agentName: string;
  busy: boolean;
  onRemove: () => Promise<unknown>;
}) {
  const { t } = useT("projects");
  const { t: common } = useT("common");
  const [open, setOpen] = useState(false);
  const [error, setError] = useState(false);

  const remove = async () => {
    if (busy) return;
    setError(false);
    try {
      await onRemove();
      setOpen(false);
    } catch {
      setError(true);
    }
  };

  return (
    <AlertDialog open={open} onOpenChange={(next) => {
      if (busy) return;
      setError(false);
      setOpen(next);
    }}>
      <AlertDialogTrigger render={<IconButton size="icon-xs" label={t(($) => $.agent_workdirs.remove, { agent: agentName })} disabled={busy} />}>
        <Trash2 />
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t(($) => $.agent_workdirs.remove_title)}</AlertDialogTitle>
          <AlertDialogDescription>{t(($) => $.agent_workdirs.remove_description, { agent: agentName })}</AlertDialogDescription>
        </AlertDialogHeader>
        {error && <p role="alert" className="text-caption text-destructive">{t(($) => $.agent_workdirs.remove_error)}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>{common(($) => $.cancel)}</AlertDialogCancel>
          <Button variant="destructive" disabled={busy} aria-busy={busy} onClick={() => void remove()}>
            {busy && <Spinner className="size-3.5" aria-hidden="true" />}
            {common(($) => $.delete)}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
