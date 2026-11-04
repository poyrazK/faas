# ADR-20261027090013 · Sandbox sessions: sandbox-as-a-tool for agent harnesses, and a GregaleSandboxClient

- **Status:** proposed.
  - The owner confirmed the §5 billing rule on 2026-10-18 (D15).
  - On 2026-10-19 the owner added three things to the first release (D18): root inside the guest (§10), exposed ports and preview URLs (§11), and an E2B-compatible shim (§12).
- **Date:** 2026-10-18; amended 2026-10-19
- **Amended by ADR-20261027090014** (when accepted): agentd owns session state and idle timers, sessions run on the agent node pool, `web` mode follows ADR-20261027090014 §4, and sessions gain `kind`.
- **Amended by ADR-20261027090019** (proposed), §2: a session's manifest may name a context instead of `gitRepo`.
- **Amends (when accepted):** ADR-20261027090014 §5. Exposed ports and preview URLs move from its phase 2 into the first release, for ephemeral sessions.
- **Builds on:**
  - ADR-20261027090010 (agent runs), ADR-20261027090011 (average used RAM), ADR-20261027090012 (pausing while waiting);
  - ADR-1151 (pause tiers);
  - ADR-230 and ADR-1037 (app tasks and the interactive attach data path);
  - ADR-171 (sanitized snapshots, durable intents under a lease) and ADR-1161 (image runs);
  - ADR-031, ADR-361, ADR-372 (egress);
  - ADR-226 and related (platform tenants);
  - ADR-005 (snapshots are a cache).
- **Decision:**
  - Add the **sandbox session**: a live microVM that an agent harness running elsewhere uses as its computer.
  - The harness, typically a seller's own loop in its backend, drives it through:
    - create;
    - exec, with an optional pseudo-terminal;
    - read, write and list files;
    - persist and hydrate the workspace;
    - close.
  - Sessions use the same images, presets, isolation, tenancy, limits and meter as agent runs.
  - Time between commands is paused and frozen, and is not billed.
  - Gregale publishes `GregaleSandboxClient`, which implements the OpenAI Agents SDK's sandbox client and session interfaces (openai/openai-agents-js `packages/agents-core/src/sandbox`, read at commit `9687b544c8`).
  - **Commands may run as root inside the guest** (§10). The VM stays the boundary, and every host-side control is unchanged.
  - **Sessions can expose HTTP ports** on preview URLs (§11). They are private by default, behind a preview token.
  - **An E2B-compatible shim** (§12) maps the E2B SDK's common calls onto this API, so code written for E2B moves by changing an import and a key.
  - Every surface is gated by the internal `agents` capability (ADR-20261023115741).
- **Why:**
  - **Sellers bring their own harness.** Agent sellers mostly build on frameworks (LangGraph, the OpenAI Agents SDK, the Claude Agent SDK, Mastra) rather than running Claude Code or Codex. They keep the trusted harness outside the sandbox (the loop, model calls, approvals, credentials) and send file and shell work into it.
  - **The SDK has formalized the split.** The OpenAI Agents SDK ships pluggable clients for E2B, Modal, Daytona, Vercel, Cloudflare, Blaxel, Runloop, Docker and local hosts. Gregale is absent from the place where those builders choose compute.
  - **This is where idle-free billing matters most.** A sandbox sits idle while the harness waits on the model between tool calls, often most of the session.
  - **Migration needs parity on the common cases** (owner, 2026-10-19). Sandbox users expect `sudo`, a URL for the dev server their agent starts, and code that already targets E2B. §13 maps today's sandbox workloads to Gregale.
- **Consequences:**
  - A new `sandbox_sessions` table and state machine.
  - A sandbox server mode in guest-init.
  - A vmmd `SandboxSession` bidi RPC that generalizes `AttachAppTask` (ADR-1037).
  - gateway routes for the data plane.
  - An npm package, and plan limits in `pkg/api/limits.go`.
  - Sandbox sessions become eligible for quiet pause between commands, which amends ADR-1151's exclusion of exec sessions.
  - **Root inside the guest.** Root needs a per-netns anti-spoofing rule and a guest kernel that cannot load modules (§10).
  - **Preview URLs.** Previews need a separate registrable domain, per-port DNAT in each VM's netns, and a gateway route keyed by host name (§11).
  - **The shim.** Two packages (TypeScript and Python), and a template import from `e2b.Dockerfile` (§12).
- **Rejected alternatives:**
  - **Only running whole harnesses (ADR-20261027090010).** Sellers' loops live in their own backends.
  - **Containers.** The VM boundary is the reason a seller would choose Gregale.
  - **Memory images as the workspace snapshot.** The SDK's snapshots are workspace contents. A per-session memory image writes the VM's whole RAM, because there is no diff-snapshot support.
  - **Carrying exec I/O through apid or schedd.** apid is not a data plane, and ADR-989 rejected schedd carrying request bodies.
  - **A Gregale-only harness protocol instead of the SDK interfaces.** Distribution comes from the SDKs.
  - **Cloning E2B's wire protocol** (its control API and in-sandbox daemon), so the stock E2B SDK would work unchanged. It would tie Gregale's API to another company's protocol changes. A shim over Gregale's own API keeps one contract.
  - **Root only through a sudo binary, with no `run_as`.** Harnesses and SDKs pass a user per command, and E2B's `user` option maps directly to it.

## Context

ADR-20261027090010 runs a harness to completion inside one VM. The 2026-10-18 review of the market found a second shape that is at least as common among agent sellers. The harness runs in the seller's backend and calls a sandbox API per tool call, keeping one sandbox per user session alive across turns.

The OpenAI Agents SDK's sandbox agents formalize that shape:
- the runner owns approvals, tracing and resume;
- the sandbox session runs commands and changes files;
- a `Manifest` describes the starting workspace;
- snapshots save and restore workspace contents;
- `SandboxClient` and `SandboxSession` are the provider contract.

Gregale already has every piece except a long-lived, multi-command session:
- microVMs restored from sanitized snapshots (ADR-171);
- one-shot commands in fresh VMs (ADR-230);
- a byte path from a client through the gateway and vmmd to guest-init (ADR-1037);
- pause tiers (ADR-1151);
- platform tenants (ADR-226).

## Decision

### 1. Session model

- **States:** `queued → starting → ready ⇄ frozen`, then one of `closed`, `expired` or `failed`, all terminal.
  - `ready` means the VM is up and guest-init's sandbox server is listening.
  - Quiet pause is vmmd-local inside `ready`, as in ADR-1151.
- **Owners:** apid inserts the intent. agentd claims it under a lease (ADR-171 pattern) and owns every later transition (ADR-20261027090014). schedd owns the instance.
- **Kind:** every session carries `kind: ephemeral | persistent`. `persistent` (volumes, memory pause, multi-day lifetimes) is refused with a clear error until its own ADR (ADR-20261027090014 §5).
- **Lifetime:**
  - A session ends at `close`, at `SandboxMaxLifetimeSec`, or after `SandboxIdleExpirySec` with no calls.
  - Its workspace is discarded when it ends, unless it was persisted (§3).
  - Snapshots stay a cache (ADR-005): a session that fails to restore cold-boots the same image.

### 2. Images and workspace

- **Images:**
  - A session starts from the platform agent-base drive0 (ADR-20261027090010 §2), which has common tools and the preset harnesses, or from a seller image built and cached per account.
  - Presets are restored from sanitized snapshots taken before any tenant data enters.
- **Workspace:**
  - The workspace is `/workspace`, on a scratch drive sized by plan (`SandboxWorkspaceMaxMB`).
  - guest-init resolves every file path inside the guest. It confines paths to the workspace and rejects symlink escapes.
- **Manifest entries:**
  - `file`, `dir`, `localFile` and `localDir` arrive as one archive and are unpacked by guest-init.
  - `gitRepo` uses the ADR-20261027090010 §3 checkout path (githubd archive at a SHA, per-repository cache), so the session holds no git credentials.
  - `s3Mount`, other mounts, and `extraPathGrants` with a `hostPath` are refused with a clear error. Guests get block devices only, never shared host directories.

### 3. API

Control-plane routes are served by apid and gated. Data-plane routes are served by `gatewayd-internal` and forwarded to the owning node's vmmd.

| Route | Plane | Purpose |
|---|---|---|
| `POST /v1/sandboxes` | control | Create. Body: `{image \| preset, manifest?, ram_mb?, tenant?, network, idle_timeout_s?, snapshot_id?, env?, labels?, ports?}`. Returns `{id, state, expires_at, token}` |
| `GET /v1/sandboxes`, `GET`/`PATCH`/`DELETE /v1/sandboxes/{id}` | control | List (filter by label); read state (re-mints the token on resume); change the idle timeout or lifetime within plan limits; close |
| `POST /v1/sandboxes/{id}/exec` | data | `{cmd, workdir?, env?, tty?, background?, timeout_ms?, max_output_bytes?, run_as?}`. `run_as` is `user` (default) or `root` (§10). Returns `{stdout, stderr, exit_code, wall_time_s, pty_session_id?}` with SSE streaming, or `{handle}` when `background` |
| `GET …/exec`, `GET`/`DELETE …/exec/{handle}` | data | List, read the output of, and kill background commands |
| `POST /v1/sandboxes/{id}/stdin` or the ADR-1037 WebSocket | data | Write to a pseudo-terminal command and resize it; read output since the last call |
| `GET`/`PUT /v1/sandboxes/{id}/files?path=`, `GET …/files:list?path=`, `POST …/files:remove`, `…/files:mkdir`, `…/files:rename` | data | Read, write, list, remove, create directories and rename inside the workspace |
| `POST /v1/sandboxes/{id}/ports`, `GET …/ports`, `DELETE …/ports/{port}` | control | Expose, list and close preview URLs (§11) |
| `POST /v1/sandboxes/{id}/workspace:persist` | data | Archive the workspace to object storage. Returns `{snapshot_id, bytes}` |
| `POST /v1/sandboxes/{id}/workspace:hydrate` | data | Unpack an uploaded archive into the workspace |

- **Tokens:** apid mints the session token: short-lived, bound to sandbox, account and tenant, and revoked at close, like ADR-1037's attach tokens.
- **Gating:** a data-plane call without a valid token is indistinguishable from an absent route.
- **Exposed ports** follow §11.

### 4. Ownership and data path

- **apid:** session intents, tokens, limits, gating.
- **agentd** (ADR-20261027090014): claims session intents under a lease, owns every session transition, and runs the idle timers. It asks schedd to place, freeze, thaw and tear down the instance.
- **schedd:**
  - admits a session as `KindAgentRun` on the agent node pool (ADR-20261027090014 §2);
  - boots it through the execution restore path, with cold boot as the fallback;
  - owns the instance's state, including ADR-1151's frozen state.
- **vmmd:** a `SandboxSession` bidi RPC that generalizes `AttachAppTask`. It carries:
  - exec, stdin, file operations and archive streams;
  - per-command timeouts and output caps;
  - the activity signal for pausing;
  - the ADR-20261027090011 memory integral.
- **guest-init:** a sandbox mode that serves many commands over vsock, with or without a pseudo-terminal. File operations and archive creation and extraction all happen inside the guest. No host component parses guest filesystems (ADR-20261027090010 §3).
- **gatewayd-internal:** routes data-plane calls, by sandbox ID, to the owning node's vmmd, using the ADR-1037 transport.

### 5. Idle, pause and billing

- **Quiet pause.** When no command, stdin write or file operation is in flight for `QuietPauseAfterMillis` (2 s), vmmd quiet-pauses the VM. The next data-plane call resumes it through `beginActivity`. This amends ADR-1151, which never quiet-pauses exec sessions: here pausing happens only between operations.
- **Freezing.** After `SandboxIdleFreezeSec` with no calls, agentd asks schedd to freeze the session's instance (ADR-1151 frozen state). The next call thaws it; the thaw measured 115–194 ms in production on 2026-10-15.
- **Expiry.** After `SandboxIdleExpirySec` with no calls, the session expires.
- **Billing:**
  - The ADR-20261027090011 meter applies (average private memory, 256 MB floor).
  - **Quiet-paused and frozen seconds are not billed** (owner decision D15, 2026-10-18). This extends D7 ("not billed while waiting") from waits on the model to waits between tool calls. vmmd excludes the quiet-paused seconds from the run's integral, as in ADR-20261027090012. Frozen seconds have no VM activity to bill. The financial model must reflect this alongside `k`.
- **Admission.** Paused sessions count no active vCPU (ADR-1151 §3). Memory stays booked under measured admission.

### 6. Network

| `network.mode` | Egress |
|---|---|
| `none` | None. The ADR-989 relay is available only if the agent version grants integrations |
| `registries` (default) | Package registries and git hosts (npm, PyPI, crates.io, GitHub) on TCP 443, through an ADR-031 allowlist under ADR-361 |
| `web` | General web, through the agent egress gateway and politeness proxy under the acceptable-use policy (ADR-20261027090014 §4). Tier 1 managed fetch and search is reachable through the relay in every mode |

Model credentials never enter a session. The harness that calls the model runs outside it. Commands inside that need a model use the relay (ADR-20261027090010 §7).

### 7. Tenancy and limits

- **Tenancy.** Each session carries an account and a tenant.
- **Concurrency.** Sessions count against `AgentRunConcurrentPerAccount` together with agent runs, and against per-tenant limits.
- **Free plan.** Free is 0 everywhere (D9).

| Name | Hobby / Pro / Scale |
|---|---|
| `SandboxMaxLifetimeSec` | 3,600 / 14,400 / 86,400 |
| `SandboxIdleFreezeSec` | 30 |
| `SandboxIdleExpirySec` | 600 (equal to `FrozenTTLSeconds`) |
| `SandboxExecTimeoutMaxSec` | 300 / 1,800 / 3,600 |
| `SandboxExecOutputMaxBytes` | 1 MiB / 4 MiB / 16 MiB |
| `SandboxWorkspaceMaxMB` | 2,048 / 8,192 / 20,480 |
| `SandboxSnapshotBytesPerAccount` | 1 GB / 10 GB / 50 GB |
| `SandboxSnapshotRetentionDays` | 7 / 30 / 90 |
| `SandboxExposedPortsMax` | 2 / 5 / 10 per session |
| `SandboxPreviewRequestsPerSec` | 20 / 50 / 200 per session |
| `SandboxPublicPreview` | off / on / on |

RAM per session follows `AgentRunRAMMBMax`.

### 8. `GregaleSandboxClient` (OpenAI Agents SDK, TypeScript)

- **Package.** `@gregale/agents-sandbox`, in the `sdk/node` workspace, with `@openai/agents` as a peer dependency pinned to tested versions. It is not exported or packed until launch (the gating rule).
- **Interfaces.** It implements `SandboxClient` with `backendId: 'gregale'`, plus `create`, `delete`, `serializeSessionState`, `deserializeSessionState` and `resume`. It also implements `SandboxSession`.

| SDK | Gregale |
|---|---|
| `create({ manifest })`, `applyManifest`, `materializeEntry` | `POST /v1/sandboxes`, then an archive upload for files and directories, and a server-side checkout for `gitRepo` |
| `exec`, `execCommand` | `POST …/exec` |
| `writeStdin`, `supportsPty()` → true | `POST …/stdin` or the ADR-1037 WebSocket |
| `readFile`, `listDir`, `pathExists`, `directoryExists` | `GET …/files`, `GET …/files:list` |
| `persistWorkspace`, `hydrateWorkspace` | `…/workspace:persist`, `…/workspace:hydrate` |
| `running`, `stop`, `shutdown`, `close`, `delete` | `GET`/`DELETE /v1/sandboxes/{id}` |
| `serializeSessionState` | `{backendId, sandboxId, apiBase}` only. The token is never serialized; `resume` re-mints it with the developer's API key |
| `resolveExposedPort` | `POST …/ports` with `private` visibility. Returns the preview URL and its token (§11) |

- **Mounts.** Unsupported mounts and host-path grants fail at `create` with an error that names the entry.
- **Distribution.** The package is published by Gregale first. It is offered upstream to `@openai/agents-extensions` once it is stable. A Python client follows if the Python SDK exposes the same contract (to be checked).
- **Example.** A sandbox agent doing PR triage, with the model call in the seller's process and the repository work in a Gregale session, is the acceptance example.

### 9. Security

- **Isolation.** Sessions have the same VM boundary as agent runs: jailer with a unique uid/gid, chroot, seccomp, cgroup v2 `memory.max`, block devices only, virtio-rng. Commands are untrusted.
- **Files.** Paths are confined to the workspace inside the guest.
- **Limits.** Output caps, per-command timeouts and PID limits apply, the same as for app tasks.
- **Tokens.** They are session-scoped and short-lived. Serialized SDK state carries none.
- **Network.** Egress follows §6. The same abuse controls apply as for web agents.
- **Root.** Root inside the guest follows §10. Host-side controls do not depend on the guest's user.
- **Inbound.** Preview traffic follows §11: its own domain, a token by default, and rate limits.

### 10. Root inside the guest

- **Option.** `exec.run_as` is `user` (the default: uid 1000, `app`) or `root`.
  - It applies to sessions only. Agent runs keep the harness as `app` (ADR-20261027090010).
  - The agent-base image gives `app` passwordless `sudo`, so tools that call `sudo` work.
  - A seller image can set `sandbox.default_user`.
- **What root can do:** anything inside its own VM. It can:
  - install packages;
  - change files outside `/workspace`, in the overlay, which is discarded at close;
  - bind any port and run services;
  - use raw sockets;
  - mount images inside the guest.

  The files API still confines its paths to the workspace, because those paths come from the host side.
- **What stays out of reach:** everything that enforces isolation runs outside the guest, so the guest's user does not change it:
  - the VM boundary: KVM, the jailer uid and chroot, seccomp, cgroup v2 `memory.max`;
  - host-side egress policy: nftables on the host side of the netns (ADR-361, ADR-372, ADR-20261027090014);
  - the billing samples, taken from the host cgroup;
  - credentials, held host-side by the relay;
  - snapshot sanitization, and the post-restore reseed and clock step, which run before readiness.
- **New requirements:**
  - **Anti-spoofing.** Each VM's netns accepts from `tap0` only source `10.0.0.2` and the VM's MAC. If the rule set does not have this yet, add it. A metal test sends spoofed packets as root and expects them dropped.
  - **No kernel code.** The guest kernel is built without loadable-module support, or with module loading locked, so root cannot load kernel code.
  - **No platform secrets in session guests.** Guest-owned platform state (`/etc/faas`, guest-init sockets) is no more trusted than any guest message, which vmmd already assumes. Root can break guest-init only for its own session; health checks then fail the session and it is torn down.
- **Registries.** Root makes OS package installs common. The `registries` network mode adds the Alpine, Debian and Ubuntu mirrors to its allowlist.
- **The security rule.** CLAUDE.md's "nothing runs as root except vmmd" is about host processes. Root inside a guest stays inside the VM boundary, and this ADR records that reading explicitly.
- **Metal tests.** As root, a session:
  - cannot reach RFC1918, link-local or metadata addresses;
  - cannot send mail ports;
  - cannot spoof its source address;
  - cannot exceed `memory.max`;
  - can run `sudo apk add` or `apt-get install` in `registries` mode.

### 11. Exposed ports and preview URLs

- **API.** `POST /v1/sandboxes/{id}/ports {port, visibility}`, or `ports` at create, returns `{port, url, token?, expires_at}`.
  - The URL is `https://<port>-<sandbox id>.<preview domain>`.
  - `visibility` is `private` (the default) or `public`.
  - Ports close when the session ends, or on `DELETE …/ports/{port}`.
- **Protocol.** HTTP/1.1 and WebSocket, with TLS at the upstream edge.
  - Raw TCP and UDP are not in this release.
  - Guest-init's own ports (the relay on 2774, profiles on 9191, and the metadata, identity and tracing listeners) are refused.
- **Path.**
  1. The upstream TLS edge sends the request to `gatewayd-public`, still the only public listener (ADR-070).
  2. `gatewayd-public` passes it to `gatewayd-internal`, which routes by host label `<port>-<id>` to the owning node's vmmd, using the same sandbox-to-node map as the data plane.
  3. In the VM's netns, a per-port DNAT rule forwards it to the guest at `10.0.0.2:<port>`. This generalizes `appPortDNATRules` (`pkg/netns/config.go`) from `:8080` to each exposed port, and keeps the identical inner network world (ADR-009).
- **Wake and billing.**
  - A request to a quiet-paused or frozen session resumes it, as a forwarded app request does (ADR-1151).
  - The request counts as activity for the idle timers.
  - It is billed by §5: unpaused seconds only.
- **Auth.**
  - **`private`.** It needs the preview token, a short-lived signed token returned by `POST …/ports`. A client sends it as `Authorization: Bearer`, or once as `?gregale_preview_token=`, which sets a host-only cookie on that preview host.
  - **`public`.** It needs no token, and is allowed on Pro and Scale only (`SandboxPublicPreview`).
  - **Header stripping.** gatewayd strips Gregale credentials and cookies from inbound requests before forwarding.
- **Domain isolation.**
  - Previews use their own registrable domain, listed on the Public Suffix List, so a preview page cannot read or set cookies for gregale.dev or for another preview.
  - Preview responses are not cached at the edge.
- **Abuse.**
  - Public previews are web-facing content under the acceptable-use policy (ADR-20261027090014 §4.5), with phishing and malware takedown.
  - Rate limits apply per session (`SandboxPreviewRequestsPerSec`).
  - Sandbox IDs are random, but a URL is not a secret: private ports rely on the token.
- **Metal tests:**
  - a dev server started as root answers on its preview URL;
  - a frozen session wakes on a preview request;
  - a private port without a token gets the absent-route response;
  - a closed session's URL stops answering.

### 12. E2B-compatible shim

- **Packages.** `@gregale/sandbox-e2b` (TypeScript) and `gregale-sandbox-e2b` (Python) expose the E2B SDK's common classes and method names on top of this API. Code moves by changing the import and the API key; supported calls keep their signatures.
- **Mapping:**

| E2B call | Gregale |
|---|---|
| `Sandbox.create({template, timeoutMs, envs, metadata})` | `POST /v1/sandboxes`: the template is a seller image or preset, `timeoutMs` the idle timeout, `envs` the session env, `metadata` the labels |
| `Sandbox.connect(id)`, `Sandbox.list()` | `GET /v1/sandboxes/{id}` (re-mints the token), `GET /v1/sandboxes` |
| `sandbox.setTimeout(ms)`, `sandbox.kill()` | `PATCH`, `DELETE /v1/sandboxes/{id}` |
| `commands.run(cmd, {background, cwd, envs, user, timeoutMs, onStdout, onStderr})` | `POST …/exec`. `user: 'root'` maps to `run_as: root`; output streams over SSE; `background` returns a handle |
| `commands.list()`, `commands.kill(pid)` | `GET …/exec`, `DELETE …/exec/{handle}` |
| `pty.create`, `sendInput`, `resize` | a pseudo-terminal command, `…/stdin` or the WebSocket |
| `files.read/write/list/exists/remove/makeDir/rename` | the files routes |
| `getHost(port)` | `POST …/ports`. The visibility comes from a shim option (default `private`); for private ports, `sandbox.previewToken(port)` returns the token to send |
| `betaPause`, `resume` | Not needed for idle time, which ephemeral sessions pause on their own (§5). Explicit long pauses need `persistent` sessions (ADR-20261027090014 §5) and fail with a clear error |
| `files.watchDir`, Code Interpreter `runCode`, the Desktop SDK | Unsupported in this release. Each fails with an error naming the call and the Gregale alternative |

- **Templates.** `gregale sandbox template build` accepts an `e2b.Dockerfile`, and reads the start and ready commands from `e2b.toml`.
  - It builds in a builder microVM (ADR-003).
  - It runs the start command before the warm snapshot (ADR-20261027090015 §1), so sessions start ready.
  - The result is a per-account seller image (ADR-20261027090010).
- **Documented differences:**
  - default egress is `registries`, not open internet;
  - mail ports are always blocked;
  - plan limits apply (§7);
  - `files.watchDir` is unsupported;
  - private preview URLs need the token.
- **Provenance.** The shim is written from E2B's public documentation, and copies no E2B code. "E2B" is used only to describe compatibility. The packages are not published until launch (the gating rule).

### 13. Workload coverage and migration

What sandbox users run today, and where it lands on Gregale.

| Workload on today's sandboxes | Gregale | Release |
|---|---|---|
| Coding agents: clone, install, edit, test, open a PR | Agent runs (ADR-20261027090010) with the per-repository checkout cache (ADR-20261027090010 §3), or sessions | first |
| A tool sandbox for a harness running elsewhere (OpenAI Agents SDK, LangGraph) | Sessions and `GregaleSandboxClient` | first |
| Code written against the E2B SDK | The E2B-compatible shim (§12) and template import | first |
| OS package installs at run time (`sudo`, `apt`, `apk`) | Root inside the guest (§10) in `registries` mode | first |
| Dev servers and previews: agents testing the web app they build | Exposed ports and preview URLs (§11) | first |
| Short untrusted code execution (playgrounds, judges, user scripts) | Sessions, or functions and image runs, which exist today | first |
| Headless browser agents | The tier 2 browser preset, Pro and up (ADR-20261027090014 §4.3) | first, after the egress gateway |
| Code interpreter: stateful Python cells and charts | A stateful kernel preset (open question 5); until then, `exec` with files | follow-up |
| Evals and RL rollouts, hundreds or thousands in parallel | Sessions or runs fanned out from one snapshot; needs a contract tier above `AgentRunConcurrentPerAccount` | first, by contract |
| Long-lived agents: days, memory-preserving pause, volumes | `persistent` sessions (ADR-20261027090014 §5) | phase 2 |
| Docker inside the sandbox (compose, testcontainers) | Not designed (open question 6) | later |
| Full desktop and computer use (VNC) | Not designed | later |
| GPU workloads | Out of scope: no GPU passthrough on Gregale's Firecracker hosts | — |
| Windows, or custom kernels | Out of scope | — |

**Migration path:**

- **Swap the client.** Use `GregaleSandboxClient` (OpenAI Agents SDK) or the E2B shim.
- **Rebuild the templates** with `gregale sandbox template build`.
- **Choose a network mode per workload.** Most need `registries` or `web`.
- **Move persisted workspaces** with `workspace:hydrate`.
- **Compare before switching.** A pricing calculator reads a provider's usage export (sandbox-hours, vCPU, GB) and shows the Gregale bill, where waiting time is free. A shadow mode runs the same tasks on both providers and compares latency and cost.

## Metrics (§12 additions)

- `sandbox_sessions_total{outcome}`
- `sandbox_session_start_seconds`
- `sandbox_exec_total{outcome}`
- `sandbox_exec_seconds`
- `sandbox_session_paused_seconds_total`
- `sandbox_session_billed_gb_seconds_total`
- `sandbox_workspace_archive_bytes`
- `sandbox_exec_total{run_as}`
- `sandbox_ports_open`
- `sandbox_preview_requests_total{visibility,outcome}`
- `sandbox_e2b_shim_calls_total{method,outcome}` (from the SDK's own telemetry opt-in, never from guests)

## Rollout

Each step is one PR on a local branch until the owner asks for PRs, all behind the `agent_runs` switches. VM-lifecycle steps run `make test-metal` and `make leakcheck`.

1. This ADR. The owner confirmed §5's billing rule (D15); the financial-model update is still owed.
2. Migrations and the apid `sandboxes` domain: intents, tokens, gating.
3. guest-init sandbox server mode: exec (with `run_as` and background handles), pseudo-terminal, files, archives. The netns anti-spoofing rule and a guest kernel that cannot load modules come with the root metal tests (§10).
4. vmmd `SandboxSession` and gateway routing, on the ADR-1037 transport.
5. agentd session lifecycle (idle timers, freezing, expiry) and schedd placement from preset snapshots, with teardown.
6. Network modes `none` and `registries`.
7. Workspace persist and hydrate.
8. Exposed ports and preview URLs: the preview domain, per-port DNAT, the gateway host route, tokens, and rate limits (§11).
9. `GregaleSandboxClient` (with `resolveExposedPort`) and the PR-triage example.
10. The E2B-compatible shim, template import, and a migration guide with the coverage table (§12, §13).
11. Billing, with ADR-20261027090011.

Long-lived paused sessions (E2B-style memory pause) need their own ADR.

## Open questions

1. Whether sessions share the agent-run concurrency pool (proposed) or get their own.
2. A Python client for the Agents SDK, and adapters for LangGraph and Mastra.
3. The preview domain's name.
4. Sessions that outlive `SandboxIdleExpirySec` with their memory kept, which depend on single-use per-VM snapshots.
5. A stateful Python-kernel preset (Jupyter protocol) for code-interpreter users, and the shim's `runCode`.
6. Docker inside the guest, which needs the guest kernel's cgroup and overlay support for nested containers.
7. Raw TCP ports, for databases and language servers.
