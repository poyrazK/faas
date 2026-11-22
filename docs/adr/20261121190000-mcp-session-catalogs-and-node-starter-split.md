# ADR-20261121190000: Per-session found tools, live capability refresh, and a Node MCP starter without Tasks

Date: 2026-11-21
Status: Accepted
Amends: ADR-20261121140000 §3 and §4

## Context

ADR-20261121140000 left three gaps:

1. **Found tools were server-wide.** `gregale_find_tools` registered its
   matches on the MCP server, so on a hosted grant every session of that
   grant listed them, and one session's searches could push another
   session's tools out of the 128-tool cap.
2. **Capabilities were read once.** A capability released or withdrawn
   while a connection was open changed nothing until the next connection.
3. **The Node starter carried Tasks.** `gregale init mcp-node` produced
   about 12,700 lines, roughly 70% of them durable MCP Tasks code (store,
   runtime, workers, release adapters) that `app.js` and `server.js`
   imported even for a plain tools server, with Tasks disabled.

A related CLI mismatch: personal keys were `gregale key create <label>`,
organization keys `gregale org keys create --label <text>`.

## Decision

### 1. Every selected tool is registered; listing is per session

Above the cap, all selected tools are registered with their handlers and
checks. A receiving middleware narrows each session's `tools/list` page to
core tools, `gregale_find_tools` and the tools that session found. A search
records its matches for the calling session only, oldest dropping out at the
cap, and re-registers them so the SDK sends
`notifications/tools/list_changed`. A tool a session has not found is still
callable by name: the cap governs what is listed, not what is permitted.
Found-tool records of closed sessions are pruned on the next search.

### 2. Capabilities refresh on `tools/list`

When a capability-gated tool is selected, `tools/list` re-reads
`GET /v1/capabilities` at most every five minutes with the request's
context. Tools of a capability that became available are registered;
those of one that became unavailable are removed (and dropped from every
session's found list). Either change sends `list_changed`. A failed read
keeps the current catalog.

### 3. `mcp-node` without Tasks; `mcp-node-tasks` with them

The previous starter becomes `mcp-node-tasks`, unchanged in behavior. Its
server is split into `app-core.js` (no Tasks import; the Tasks module is an
option) and a three-line `app.js` that passes `tasks.js`. The new `mcp-node`
starter ships its own `server.js`, `gregale-mcp.json` (no `tasks` block, no
`build_report` or `task://` entries), `package.json` without `pg`, and a
shorter README; `init` copies `app-core.js` (as `app.js`), the shared modules
and the Task-free tests from `mcp-node-tasks`, so shared code has one copy.
A new project is about 3,400 lines including its lockfile.

`gregale mcp init --runtime node --tasks` and native Task releases use
`mcp-node-tasks`; their errors name it. Existing projects are unaffected.

### 4. `gregale org key create <label>`

`org key` is a noun alias of `org keys`, and the label may be positional, as
in `gregale key create <label>`. `--label` still works; naming two different
labels is refused.

## Consequences

- Sessions on one hosted grant no longer see or evict each other's found
  tools.
- A capability release reaches open connections within five minutes.
- A plain Node MCP project is about a quarter of its former size; Tasks
  users choose `mcp-node-tasks` explicitly.
- CI runs the plain starter's tests and keeps the Task suites, restart
  qualification and SDK qualification on `mcp-node-tasks`.

## Amendment 1 (2026-11-21): Go and Python starters

`mcp-go` and `mcp-python` split the same way. The previous starters become
`mcp-go-tasks` and `mcp-python-tasks`, unchanged. The plain starters share
their server code through `sharedTemplateCopies` and replace the Task modules
with a small disabled runtime: `tasks_off.go` (shipped as `tasks_off.go.tmpl`,
since this repository compiles the template directories as Go packages) and a
stub `tasks.py`. Their configs drop the `tasks` block and the `task://`
resource scope; `build_report` stays as a synchronous sample tool. A plain Go
project is about 5,800 lines (was about 11,000); a plain Python project about
5,500 (was 7,500), because the shared `server.py` keeps its Task branches.
The plain Go `go.mod` still lists `pgx`: dropping it changes the pruned module
graph and needs `go.sum` entries only a download can add, and `go mod tidy`
removes it. `gregale mcp init --tasks` uses the runtime's `-tasks` starter. CI
runs the plain and Tasks starters' tests for all three runtimes.
