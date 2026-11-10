# Move sandbox workloads to Gregale

> Internal until sandbox sessions launch (ADR-20261027090013). This page is not in `docs/customer-pages.json`, so it is not published.

Gregale sandbox sessions are Firecracker microVMs with a `/workspace`, root inside the guest, exposed ports, and persisted workspaces. You are billed while they run; idle sessions freeze on their own and cost nothing while frozen. This page maps what you run on other sandbox providers today to Gregale, and how to move it.

## What lands where

| What you run today | On Gregale | Status |
|---|---|---|
| Coding agents: clone, install, edit, test, open a PR | Agent runs, or sandbox sessions | Available at launch |
| A tool sandbox for a harness running elsewhere (OpenAI Agents SDK) | Sandbox sessions and `@gregale/agents-sandbox` | Available at launch |
| Code written against the E2B SDK | `@gregale/sandbox-e2b`, an E2B-compatible client | Available at launch; Python client to follow |
| OS package installs at run time (`apk`, `apt`, `sudo`) | Root inside the guest, in `registries` network mode | Available at launch |
| Dev servers and previews | Exposed ports with preview URLs, private behind a token by default | Available at launch |
| Short untrusted code (playgrounds, judges, user scripts) | Sandbox sessions, or functions and image runs | Available at launch |
| Custom templates (`e2b.Dockerfile`, `e2b.toml`) | `gregale sandbox template publish` builds the template; sessions start from it by name, or from your own OCI image (`image` on create) | Available at launch |
| Open internet egress | The `web` network mode | Not yet: `none` and `registries` (package registries and GitHub over 443) only |
| Headless browser agents | A browser preset, Pro and up | After the agent egress gateway |
| Code interpreter: stateful Python cells and charts | A stateful kernel preset | Follow-up; until then, run Python with `commands.run` and files |
| Evals and RL rollouts, hundreds or thousands in parallel | Sessions fanned out from one snapshot | By contract, above the plan's concurrency |
| Long-lived agents with memory-keeping pause and volumes | Persistent sessions | Phase 2 |
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

2. Set `GREGALE_API_KEY` (or pass `apiKey`) in place of the E2B key.
3. Check the differences below against your code.

| E2B call | On Gregale |
|---|---|
| `Sandbox.create({ template, timeoutMs, envs, metadata })` | `template` is a preset (`base` means the default preset) or the name of a template you built with `gregale sandbox template publish`; `timeoutMs` is the idle time before the session freezes; `envs` go with every command; `metadata` become labels |
| `Sandbox.connect(id)`, `Sandbox.list()`, `Sandbox.kill(id)` | The same. `connect` mints a fresh session token with your API key |
| `sandbox.setTimeout(ms)`, `sandbox.kill()`, `sandbox.isRunning()`, `sandbox.getInfo()` | The same |
| `commands.run(cmd, { background, cwd, envs, user, timeoutMs, onStdout, onStderr })` | The same. `user: 'root'` runs as root; with callbacks the output streams; a non-zero exit throws `CommandExitError` |
| `commands.list()`, `commands.kill(pid)`, `commands.connect(pid)`, `handle.wait()` | The same. A pid is local to the client object; `commands.list()` names the running commands |
| `files.read/write/list/exists/getInfo/makeDir/rename/remove` | The same inside `/workspace`; relative paths are relative to `/workspace`. Reads and writes elsewhere go through a command and are capped at 256 KB, and `list`, `getInfo` and `rename` work inside `/workspace` only |
| `getHost(port)` | Gregale exposes a port with an API call: pass `ports: [3000]` to `create`, or `await sandbox.exposePort(3000)` first. A private port needs `sandbox.previewToken(port)` as a bearer token, or once as `?gregale_preview_token=` in a browser |
| `createSnapshot()` | Persists the workspace on Gregale; create with `snapshotId` to start from it |
| `pause`, `betaPause` | Not needed: idle sessions freeze on their own and resume on the next call |
| `pty.*`, `commands.sendStdin`, `files.watchDir`, `getMcpUrl`, Code Interpreter `runCode`, the Desktop SDK | Not supported yet. Each throws `UnsupportedError` naming the call and what to use instead |

**Other differences:**

- The default network mode is `registries`, not the open internet. `allowInternetAccess: false` means `none`.
- Mail ports (25, 465, 587) and private, link-local and metadata addresses are always blocked.
- Plan limits apply: concurrent sessions, memory, lifetime, exposed ports and preview rate.
- Commands take no stdin yet.

## Move an OpenAI Agents SDK harness

Use `GregaleSandboxClient` from `@gregale/agents-sandbox` as the run's sandbox client:

```ts
const result = await run(agent, input, { sandbox: { client: new GregaleSandboxClient(), manifest } });
```

The model call stays in your process. `file`, `dir`, `local_file`, `local_dir` and `git_repo` manifest entries are supported; mounts are not yet.

## Move workspaces

Upload a tar.gz of an existing workspace with `POST /v1/sandboxes/{id}/workspace:hydrate`, or `hydrateWorkspace` in either client. To keep a workspace between sessions, persist it (`workspace:persist`, `createSnapshot()` or `persistSnapshot()`) and create the next session with its `snapshot_id`.

## Start from your own image

Create a session with `image` in place of `preset` to boot any OCI image as the session's root filesystem:

```json
POST /v1/sandboxes
{ "image": "ghcr.io/acme/agent-tools:1.4", "ram_mb": 2048 }
```

- The session answers `queued` until the image is pulled and cached, then starts. If the pull fails, the session ends with outcome `image_unavailable` and the registry's error.
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
- E2B snapshots a running start command into the template. Gregale runs it at every boot, so a session starts as fast as your start command gets ready.
- Start a session with `{ "template": "my-template" }`, or `Sandbox.create('my-template')`. Sessions boot the newest finished build; a rebuild leaves running sessions alone.
- The image needs a shell (`/bin/sh`) for commands and for `start_cmd` and `ready_cmd`.
- Each plan has a template limit (`SandboxTemplatesMax`): Free 0, Hobby 3, Pro 20, Scale 100. Template builds do not count as apps.

## Choose a network mode

| Mode | Egress |
|---|---|
| `none` | Nothing |
| `registries` (default) | npm, PyPI, crates.io, the Go module proxy, GitHub, and the Alpine, Debian and Ubuntu mirrors, over HTTPS only |
| `web` | Not available yet |
