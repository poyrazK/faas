# Move sandbox workloads to Gregale

> Internal until sandbox sessions launch (ADR-20261027090013). This page is not in `docs/customer-pages.json`, so it is not published.

Gregale sandbox sessions are Firecracker microVMs with a `/workspace`, root inside the guest, exposed ports, and persisted workspaces. You are billed while they run; idle sessions freeze on their own and cost nothing while frozen. This page maps what you run on other sandbox providers today to Gregale, and how to move it.

## What lands where

| What you run today | On Gregale | Status |
|---|---|---|
| Coding agents: clone, install, edit, test, open a PR | Agent runs, or sandbox sessions | Available at launch |
| A tool sandbox for a harness running elsewhere (OpenAI Agents SDK) | Sandbox sessions and `@gregale/agents-sandbox` | Available at launch |
| Code written against the E2B SDK | E2B-compatible clients: `@gregale/sandbox-e2b` (TypeScript) and `gregale-sandbox-e2b` (Python) | Available at launch |
| OS package installs at run time (`apk`, `apt`, `sudo`) | Root inside the guest, in `registries` network mode | Available at launch |
| Dev servers and previews | Exposed ports with preview URLs, private behind a token by default | Available at launch |
| Short untrusted code (playgrounds, judges, user scripts) | Sandbox sessions, or functions and image runs | Available at launch |
| Custom templates (`e2b.Dockerfile`, `e2b.toml`) | `gregale sandbox template publish` builds the template; sessions start from it by name, or from your own OCI image (`image` on create) | Available at launch |
| Open internet egress | The `web` network mode | Not yet: `none` and `registries` (package registries and GitHub over 443) only |
| Headless browser agents | A browser preset, Pro and up | After the agent egress gateway |
| Code interpreter: stateful Python cells and charts | A stateful kernel preset | Follow-up; until then, run Python with `commands.run` and files |
| Evals and RL rollouts, hundreds or thousands in parallel | Sessions fanned out from one snapshot | By contract, above the plan's concurrency |
| Long-lived agents with memory-keeping pause and volumes | Persistent sessions: pause, resume, fork and a `/workspace` volume (ADR-20261122132055) | Built; off until switched on |
| Docker inside the sandbox | Not designed yet | Later |
| Desktop and computer use (VNC) | Not designed yet | Later |
| GPU workloads | Out of scope: no GPU passthrough | — |
| Windows, or custom kernels | Out of scope | — |

## Move E2B code

1. Install `@gregale/sandbox-e2b` and change the import:

   ```ts
   // import { Sandbox } from 'e2b';
   import { Sandbox } from '@gregale/sandbox-e2b';
   ```

   In Python, install `gregale-sandbox-e2b` and change the import the same way:

   ```python
   # from e2b import Sandbox
   from gregale_sandbox_e2b import Sandbox
   ```

2. Set `GREGALE_API_KEY` (or pass `apiKey`, `api_key` in Python) in place of the E2B key.
3. Check the differences below against your code. The Python client keeps the same calls under their Python names (`commands.run(cmd, background=True, on_stdout=...)`, `files.make_dir`, `get_host`, `expose_port`), with times in seconds; the differences are listed after the table.

| E2B call | On Gregale |
|---|---|
| `Sandbox.create({ template, timeoutMs, envs, metadata })` | `template` is a preset (`base` means the default preset) or the name of a template you built with `gregale sandbox template publish`; `timeoutMs` is the idle time before the session freezes; `envs` are set in the session once it runs, so `Sandbox.connect` from another process sees them too, and a command's own `envs` win over them; `metadata` become labels. `sandbox.setEnvs(envs)` (Gregale only) replaces them later |
| `Sandbox.connect(id)`, `Sandbox.list()`, `Sandbox.kill(id)` | The same. `connect` mints a fresh session token with your API key |
| `sandbox.setTimeout(ms)`, `sandbox.kill()`, `sandbox.isRunning()`, `sandbox.getInfo()` | The same |
| `commands.run(cmd, { background, cwd, envs, user, timeoutMs, onStdout, onStderr })` | The same. `user: 'root'` runs as root; with callbacks the output streams; a non-zero exit throws `CommandExitError` |
| `commands.list()`, `commands.kill(pid)`, `commands.connect(pid)`, `handle.wait()` | The same. A pid is local to the client object; `commands.list()` names the running commands |
| `files.read/write/list/exists/getInfo/makeDir/rename/remove` | The same inside `/workspace`; relative paths are relative to `/workspace`. Reads and writes elsewhere go through a command and are capped at 256 KB, and `list`, `getInfo` and `rename` work inside `/workspace` only |
| `getHost(port)` | Gregale exposes a port with an API call: pass `ports: [3000]` to `create`, or `await sandbox.exposePort(3000)` first. A private port needs `sandbox.previewToken(port)` as a bearer token, or once as `?gregale_preview_token=` in a browser |
| `createSnapshot()` | Persists the workspace on Gregale; create with `snapshotId` to start from it |
| `pause`, `betaPause` | On a session created with `kind: 'persistent'`: pauses it with its memory and `/workspace` kept; `Sandbox.connect(id)` resumes it. An ephemeral session cannot pause (it throws `UnsupportedError`): it freezes on its own when idle and resumes on the next call |
| `sandbox.fork(n)` (Gregale only) | Starts `n` sessions from this one's memory and `/workspace`; see [Pause, resume and fork](#pause-resume-and-fork) |
| `pty.create({ cols, rows, onData })`, `pty.sendInput`, `pty.resize`, `pty.kill` | The same. The terminal is a background command; `onData` receives its output, polled about every 200 ms rather than pushed. Its timeout defaults to 60 s within the plan's cap, and `timeoutMs: 0` means that default, not no limit |
| `commands.run(cmd, { background: true, stdin: true })`, `commands.sendStdin(pid, data)`, `commands.closeStdin(pid)` | The same for background commands, which always take input. A foreground command takes none |
| Code Interpreter: `Sandbox.create('code-interpreter')`, `runCode(code, { language, context, onStdout, onStderr, onResult, onError, timeoutMs })`, `createCodeContext`, `listCodeContexts`, `restartCodeContext`, `removeCodeContext` | The same, from the one `@gregale/sandbox-e2b` package. Python only; each context is a Jupyter kernel. `runCode`'s `envs` is refused: set the session's with `setEnvs`. Results (`text`, `html`, `png`, `json`, …) and `onResult` come when the run ends, not as each output appears |
| `files.watchDir`, `getMcpUrl`, the Desktop SDK | Not supported yet. Each throws `UnsupportedError` naming the call and what to use instead |

**Other differences:**

- The default network mode is `registries`, not the open internet. `allowInternetAccess: false` means `none`.
- Mail ports (25, 465, 587) and private, link-local and metadata addresses are always blocked.
- Plan limits apply: concurrent sessions, memory, lifetime, exposed ports and preview rate.
- Only background commands and terminals take input; terminal output is polled, so keystrokes echo after up to about 200 ms.

**Python client differences:**

- Synchronous only: use `Sandbox`; there is no `AsyncSandbox` yet.
- A background command's `on_stdout` and `on_stderr` run while you `wait()` on the handle or iterate it (`for stdout, stderr, _ in handle`), not on a thread of their own.
- A terminal from `pty.create(PtySize(rows, cols))` gives its output the same way: as the third item when you iterate the handle, or to `handle.wait(on_pty=...)`.
- Pass `api_base` instead of `domain`. Unsupported calls raise `UnsupportedException`; a non-zero exit raises `CommandExitException`.

## Move an OpenAI Agents SDK harness

Use `GregaleSandboxClient` from `@gregale/agents-sandbox` as the run's sandbox client:

```ts
const result = await run(agent, input, { sandbox: { client: new GregaleSandboxClient(), manifest } });
```

The model call stays in your process. `file`, `dir`, `local_file`, `local_dir` and `git_repo` manifest entries are supported; mounts are not yet.

## Pause, resume and fork

A persistent session keeps its memory and its `/workspace` volume when it pauses, for days, and picks up where it was when it resumes: a running dev server, a loaded model or a REPL's state is still there. Ephemeral sessions (the default) end with their lifetime. Persistent sessions are off until they are switched on.

```json
POST /v1/sandboxes
{ "preset": "codex", "kind": "persistent", "volume_gb": 20, "vcpu": 2, "ram_mb": 4096 }
```

| Call | What it does |
|---|---|
| `POST /v1/sandboxes/{id}/pause` | Pauses the session. Its memory is captured on the node, then copied to object storage; the view shows `paused_at` and `durability` (`node`, then `remote`) |
| `POST /v1/sandboxes/{id}/resume` | Resumes it, on its node when it can, anywhere once its pause is in object storage. A data-plane call to a paused session does not resume it yet: call `resume` (or `Sandbox.connect`) first |
| `POST /v1/sandboxes/{id}/fork` `{ "count": 3, "kind": "ephemeral" }` | Starts `count` new sessions from this one's memory and `/workspace` as they are now: a running session is captured without stopping, a paused one forks from its pause. Each fork gets its own network address and tokens, and its kernel random state and clock are reseeded; forks are ephemeral unless `kind` says otherwise |
| `PATCH /v1/sandboxes/{id}` `{ "volume_gb": 50 }` | Grows the volume |
| `GET /v1/sandboxes?state=paused` | Lists paused sessions |

- A persistent session pauses on its own after `SandboxPersistentIdlePauseSec` without calls, and after running `SandboxPersistentRunMaxSec`. A paused session is deleted after `SandboxPausedRetentionDays`.
- Plan limits: `SandboxPersistentMax` persistent sessions per account, `SandboxVolumeMaxGB` per volume (10 GB by default), `SandboxPersistentRAMMBMax` and `SandboxPersistentVCPUMax` per session, `SandboxForkMax` forks per request. Free has none.
- Sessions above 4 GB or 2 vCPUs run on large nodes only.
- The volume is backed up every 10 minutes while the session runs and at each pause. If the node is lost while the session runs, the session loses its memory since its last pause and up to the last 10 minutes of `/workspace` writes; it resumes from its last pause in object storage, or boots fresh on its newest volume backup (`resumed_cold`).
- Forks share what was in memory at the capture: files in `/tmp`, caches, and random state a process seeded itself. A process that keeps its own random generator must reseed it in each fork.
- A paused session's stored bytes (`stored_bytes` in its view, `GET /v1/sandbox-storage` per hour) are metered but not billed yet. Running time is billed as for any session.
- In the clients: `Sandbox.create({ kind: 'persistent' })`, `sandbox.betaPause()`, `Sandbox.connect(id)` and `sandbox.fork(n)` in the E2B clients (`kind="persistent"`, `beta_pause()` in Python); `options.kind`, `session.stop()` (which pauses a persistent session) and `client.resume(state)` in `@gregale/agents-sandbox`.
- In the CLI: `gregale sandbox session list --paused`, `gregale sandbox session pause <id>`, `resume <id>`, `fork <id> --count N` and `delete <id>`.

## Move workspaces

Upload a tar.gz of an existing workspace with `POST /v1/sandboxes/{id}/workspace:hydrate`, or `hydrateWorkspace` in either client. To keep a workspace between sessions, persist it (`workspace:persist`, `createSnapshot()` or `persistSnapshot()`) and create the next session with its `snapshot_id`.

## Start from your own image

Create a session with `image` in place of `preset` to boot any OCI image as the session's root filesystem:

```json
POST /v1/sandboxes
{ "image": "ghcr.io/acme/agent-tools:1.4", "ram_mb": 2048 }
```

- The session answers `queued` until the image is pulled and cached, then starts. If the pull fails, the session ends with outcome `image_unavailable` and the registry's error.
- A private image takes `registry_auth` (`{"username", "password"}`; prefer a short-lived read-only token). It is stored sealed for the image's registry and never returned. `"pull": "always"` pulls a cached image again, so a moved tag such as `latest` resolves to its new image.
- Images share the image-run cache and its plan limit. A cached image a live session uses is never evicted; when every cached image is in use, create answers `image_run_cache_full`.
- Commands run as `app` (uid 1000) in `/workspace`, or as root with `user: 'root'`. The image needs a shell (`/bin/sh`) for commands.

## Build a template

Run `gregale sandbox template publish` in a directory with an `e2b.Dockerfile` (or a `Dockerfile`) and, optionally, an `e2b.toml`:

```bash
gregale sandbox template publish ./my-template
```

- The build runs in a builder microVM, like an app build. The command waits for it and prints the template's name; `gregale sandbox template list`, `get <name>` and `rm <name>` manage templates.
- From `e2b.toml` Gregale reads `template_name`, `dockerfile`, `start_cmd` and `ready_cmd`; flags of the same names override them. `cpu_count` and `memory_mb` are ignored: pick memory per session with `ram_mb`.
- `start_cmd` runs in the background, as `app`, each time a session boots; its output goes to `/tmp/gregale-start.log`. The session is ready once `ready_cmd` exits 0, polled each second for up to `--ready-timeout` seconds (60 by default, at most 600). If it never passes, the session fails.
- E2B snapshots a running start command into the template. Gregale restores the booted template from a warm snapshot and then runs the start command in each session, so no two sessions share a process's memory or random state. A session starts as fast as your start command gets ready.
- Start a session with `{ "template": "my-template" }`, or `Sandbox.create('my-template')`. Sessions boot the newest finished build; a rebuild leaves running sessions alone.
- The image needs a shell (`/bin/sh`) for commands and for `start_cmd` and `ready_cmd`.
- Each plan has a template limit (`SandboxTemplatesMax`): Free 0, Hobby 3, Pro 20, Scale 100. Template builds do not count as apps.

## Choose a network mode

| Mode | Egress |
|---|---|
| `none` | Nothing |
| `registries` (default) | npm, PyPI, crates.io, the Go module proxy, GitHub, and the Alpine, Debian and Ubuntu mirrors, over HTTPS only |
| `web` | Not available yet |
