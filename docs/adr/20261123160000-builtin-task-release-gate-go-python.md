# ADR-20261123160000: A built-in Task release gate for Go and Python workers

- Status: Accepted
- Date: 2026-11-23
- Related: durable MCP Tasks native releases (`gregale mcp tasks release`,
  `tasks-release.js` in mcp-node-tasks), MCP Tasks stack prod probe
  2026-11-22, #7936 (Go stack release migration)

## Context

`gregale mcp tasks release run` replaces a Tasks worker generation without
dropping Tasks: it migrates the schema, deploys replacement workers into a
separate app, waits until they register in the worker registry, drains the
previous workers and records a checkpoint so an interrupted release resumes.
The CLI drives deployments; the decision of when replacements are safe is
made by a gate script that ships in the Node starter (`tasks-release.js`,
`tasks-release-readiness.js`). It uses the Node store's admission tables
(`gregale_mcp_task_admission*`) to prove that every admitted handler version
stays served.

The Go and Python starters write the same worker registry
(`gregale_mcp_task_workers` with a `handlers` list of `{name, version}`) and
the same `gregale_mcp_tasks` rows, but they have no admission tables and no
database-side claim fence. So the CLI refused them, and their READMEs told
operators to roll workers by hand: deploy a separate app, watch heartbeats,
then retire the old one. That manual path is where releases drop Tasks.

The owner chose (2026-11-23) a gate built into the CLI over porting the Node
gate and its admission schema into both starters.

## Decision

1. **The CLI carries the gate for Go and Python workers.** `release run`
   detects the worker's starter (`tasks-release.js` → Node; `go.mod` +
   `task_schema.go` + `task_worker_registry.go` → Go; `tasks.py` +
   `task_schema.py` → Python). Node keeps its own gate unchanged. For Go and
   Python the CLI runs the same stages in-process
   (`cmd/gregale/commands_mcp_native_gate.go`) and reports in the same JSON
   shape, so the journal, the `start`/`drain` adapters and recovery from a
   failed run are shared:
   - take the namespace's `gregale_mcp_release:<namespace>` advisory lock (the
     same lock the Node gate takes);
   - run the starter's own migration from the worker source
     (`go run . tasks-migrate apply` / `python3 task_schema.py apply`) with
     `MCP_TASK_MIGRATION_DATABASE_URL`;
   - record the previous worker IDs and the release start time in the
     checkpoint (`<journal>.gate`, the Node gate's format);
   - start replacements through the `start` adapter and wait until **each**
     replacement is registered, not draining, heartbeating since the release
     began, and lists **every `(tool, handler_version)` that unfinished Tasks
     (`queued`, `running`, `input_required`) need**;
   - drain the previous workers through the `drain` adapter and wait until
     their registrations are gone, with readiness rechecked on every poll.
   The same readiness check replaces `tasks-release-readiness.js` before
   promotion and each park.
2. **Readiness is defined by unfinished Tasks, not by admission.** Without
   admission tables the gate cannot know which versions the web may still
   admit; it requires replacements to serve what is already queued. Each
   release rechecks after the drain, so Tasks admitted with an old version
   during the window still block the drain from completing.
3. **Workers announce themselves.** The Go and Python workers log the
   `mcp_task_worker_started` receipt (`workerID`, `shutdownTimeoutMs`) at start
   and with every 10 s capability heartbeat, as Node does, so the CLI can map
   instances to registry IDs and drain budgets. They report
   `claimFence: false`.
4. **Recovery, quarantine, retirement and hosted Task execution stay Node-only.**
   They need a claim fence or the Node restore scripts. The CLI names the step
   and points Go/Python users at the manual procedure.

## Consequences

- Go and Python stacks get the same zero-drop worker replacement as Node, with
  no schema change in either starter.
- A worker generation deployed before this change logs no receipt, so the
  first gated release from it fails at `drain_previous` with the existing
  "drain budget is missing" error; that one replacement is done manually.
- The operator's machine needs the starter's toolchain for the migration step
  (Go, or Python with the starter's requirements), as Node's gate needs
  `node_modules`.
- Weaker than Node's admission proof: a handler version the web could admit
  but no Task uses yet is not checked. Porting admission remains possible later
  without changing the CLI contract.
