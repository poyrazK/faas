# ADR-20261116182615 · Browser sessions, their warm snapshot, and sandbox tasks for `gregale_browse`

- **Status:** proposed (owner decision, 2026-11-16: "Both, ADR first")
- **Date:** 2026-11-16
- **Amends (when accepted):** ADR-20261112133503 (its "not yet: a warm snapshot with the browser already started"), ADR-20261027090013 §3 and §12 (a `browser` create option and its snapshot shape), ADR-20261030125501 §4 (a third platform tool).
- **Builds on:** ADR-20261112121456 (sandbox `web` mode), ADR-20261112133503 (Chromium in agent-base, `gregale-browser`), ADR-20261027090013 §11–§12 (exposed ports, warm snapshots), ADR-20261030125501 (stateless agents, `gregale_run`).

## Context

Since #7122, every sandbox session can start Chromium with `gregale-browser`, and the SDK's `startBrowser()` does so on demand. Two gaps remain against other agent sandbox providers:

1. **A browser session is not ready when the session is.** The first `startBrowser()` launches Chromium from a cold page cache, then exposes port 9222.
2. **A stateless agent cannot browse.** It holds nothing between invocations, so it cannot drive a session. Agent runs have no network (ADR-20261027090010), and nothing runs a command in a session on a tool's behalf.

A warm snapshot cannot hold a running Chromium. Two sessions restored from one snapshot would share its in-memory random-number state, including TLS key material (CLAUDE.md invariant 5). This is why template snapshots are captured before a template's commands run (ADR-20261027090013 §12).

## Decision

### 1. `browser: true` on create

- `POST /v1/sandboxes` takes `browser: true` for preset sessions. It needs `ram_mb` ≥ 1024 (`SandboxBrowserRAMMBMin`).
- apid exposes port 9222 privately at create (ADR-20261027090013 §11), so the create response already carries the CDP preview URL.
- The session is `ready` only once `gregale-browser` answers. vmmd runs `gregale-browser` as the session's start command and `gregale-browser --wait` as its ready command, through the template start path (`template_start`), after a cold boot or a restore alike. The commands are vmmd's constants, never caller input. A preset session's cold boot now runs commands through `template_start` too, since it has no image drive to carry `sandbox.json`.
- `sandbox_sessions.browser` records it (expand migration, default false).

### 2. The browser shape's warm snapshot

- `browser` is a dimension of the warm-snapshot shape (ADR-20261027090013 §12), so browser sessions never restore a plain preset snapshot, and plain sessions never pay for a bigger one.
- Before capturing a browser shape, vmmd runs `gregale-browser --prewarm` with the ordinary exec operation. It starts Chromium, waits for CDP, stops it, and deletes its profile, leaving Chromium's files in the guest page cache.
- Nothing that runs is captured: Chromium is stopped, and `template_start` has not run in the captured VM, so a restore starts Chromium afresh after the resume reseed.
- The snapshot is larger by the page cache Chromium touched. Restores map memory lazily, so the cost is mostly disk. The metal check measures both, plus the time to a ready browser, cold versus warm. If prewarm does not pay for itself, a follow-up drops it and the browser shape is captured like any other.

### 3. Sandbox tasks

- A **sandbox task** is a session that runs one platform command and ends. apid records it as a session with `task` (a kind and its arguments, as JSON), `browser: true`, network `web` and a short lifetime.
- agentd sends the task on the `RunSandbox` stream's start frame (`SandboxRunStart.task`, new field). vmmd runs the kind's command once the session is ready, bounded by `SandboxTaskTimeoutSec`. It reports `SandboxRunTaskDone {exit_code, output}` with output capped at `SandboxTaskOutputMaxBytes`, and closes the VM.
- agentd records the output on the session row (`task_result`, `task_exit_code`) as it finishes it.
- Kinds are a closed set, each mapping to a fixed command. Caller input reaches a command only as validated arguments, never as shell text. The first kind is `browse`: `gregale-browser fetch <url>` prints `{url, final_url, status, title, text, truncated}`.

### 4. `gregale_browse`

- A third tool in the Gregale RAP toolset (ADR-20261030125501 §4). Its arguments are `url` (http or https, public host per `api.SandboxWebHost`) and an optional `max_chars`.
- Like `gregale_run`, it needs the app's credential (workload token or a `deploy:write` key), because it spends the account's compute.
- apid admits a browse task linked to the thread and call. The task uses the call's ID as its dedupe key, so a retried invocation finds its task.
- When the task ends, apid publishes its result into the thread as the call's tool result: the fetch JSON, or the refusal or failure.
- It counts against the account's sandbox concurrency and is billed as sandbox compute (ADR-20261027090013 §5).
- It is refused with `sandbox_unsupported` while sandbox web mode is off (`FAAS_SANDBOX_WEB_NETWORK`).
- The launcher turns off Chromium's background networking (updates, sync, metrics), so a session's egress carries only the pages it loads.

### Limits (`pkg/api/limits.go`)

| Limit | Free | Hobby | Pro | Scale |
|---|---:|---:|---:|---:|
| `SandboxBrowserRAMMBMin` | — | 1024 | 1024 | 1024 |
| `SandboxTaskTimeoutSec` | 0 | 60 | 120 | 120 |
| `SandboxTaskOutputMaxBytes` | 0 | 256 KiB | 256 KiB | 256 KiB |

## Rollout

- **PR 1:** `browser: true`, its commands, and the warm browser shape.
- **PR 2:** sandbox tasks, `gregale_browse`, and the fetch mode.
- Both are expand-only and safe across release skew. An older vmmd refuses a start frame carrying a task. apid admits browser sessions only after vmmd, schedd and agentd are released (`FAAS_SANDBOX_BROWSER`, default off).
- A metal check of the warm shape comes before the switch is turned on.

## Rejected alternatives

- **Snapshotting a running Chromium.** It breaks invariant 5.
- **Prewarming every preset snapshot.** Sessions that never browse would pay the larger snapshot.
- **`gregale_browse` through agent runs.** Runs have no network, and adding one duplicates sandbox web mode's controls.
- **Running browse commands from apid over the data plane.** apid would call the data plane, which bypasses the session's owner (agentd).
