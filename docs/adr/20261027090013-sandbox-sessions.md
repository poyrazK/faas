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
  - **Preview URLs.** Previews need a separate registrable domain, a vsock relay from vmmd to the guest port, and a gateway route keyed by host name (§11).
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
| `registries` (default) | Package registries and git hosts (npm, PyPI, crates.io, GitHub) on TCP 443, through a DNS-pinned name allowlist on ADR-373's DNS-gated egress under ADR-361 (step 6) |
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

- **Package.** `@gregale/agents-sandbox`, in its own package `sdk/agents-sandbox` (amended 2026-11-05; first proposed inside the `sdk/node` workspace), with `@openai/agents-core` as a peer dependency pinned to tested versions. It is not exported or packed until launch (the gating rule).
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
  2. `gatewayd-public` passes it to `gatewayd-internal`, which routes by host label `<port>-<id>` to the owning node's vmmd.
  3. vmmd opens a `SandboxCall` `port_connect` to the guest, and guest-init connects to the port on loopback. The connection is carried over vsock as data frames, so no netns rule changes per port and the guest port is never reachable on the host's network. (Amended 2026-11-05 by the owner: this replaces the per-port DNAT first proposed here, and works in every network mode, `none` included.)
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

- **Templates.** `gregale sandbox template publish` accepts an `e2b.Dockerfile`, and reads the start and ready commands from `e2b.toml`.
  - It builds in a builder microVM (ADR-003).
  - It runs the start command before the warm snapshot (ADR-20261027090015 §1), so sessions start ready. *Amended in step 10 part three: the start command runs at each session boot, and the session is ready once the ready command passes.*
  - The result is a per-account seller image (ADR-20261027090010). *As built, the image is the newest finished build of the template's build app.*
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
- **Rebuild the templates** with `gregale sandbox template publish`.
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

   **Built (2026-11-02), on a local branch.**
   - **`sandbox_sessions`** (migration `20261110153935524`, expand) holds the intent:
     - §1's states, with CHECKs, and `kind`;
     - the preset, RAM, network mode, idle timeout, end and labels;
     - an outcome, `close_requested_at`, and `closed_at` on terminal states only;
     - lease and placement columns for agentd (step 5).
   - **Routes** (`pkg/apid/sandboxes`), under `agents`: `POST /v1/sandboxes`, `GET /v1/sandboxes` (filtered by state and by a label selector, newest first), and `GET`, `PATCH` and `DELETE /v1/sandboxes/{id}`.
     - Create takes a preset, `kind`, `ram_mb`, `tenant`, `network.mode`, `idle_timeout_s`, `lifetime_s` and labels.
     - The defaults are the preset's RAM within `AgentRunRAMMBMax`, `ephemeral`, `registries`, `SandboxIdleExpirySec` and the plan's `SandboxMaxLifetimeSec`.
     - These are refused with `sandbox_unsupported`, naming the field: `kind: persistent`; `network.mode: web`, until the egress gateway; and `image`, `manifest`, `snapshot_id`, `env` and `ports`, until their steps.
     - Free creates none, since its `SandboxMaxLifetimeSec` is 0.
     - Queued sessions share `AgentRunQueuedPerAccount` with queued runs, counted under the account's agent lock. Concurrency stays agentd's, at claim time.
     - Closing a queued session closes it. A claimed one gets `close_requested_at`, which agentd acts on (step 5).
   - **Tokens** (`pkg/sandboxtoken`) are signed and bound to the session, account and tenant. They were HMAC-signed (`sbx1`) as first built; step 5 moved them to Ed25519 (`sbx2`).
     - A token lives `SandboxTokenTTLSec` (900 s), never past the session's end.
     - Create and every read of a live session return a fresh one, so reading needs `deploy:write`; the list carries none.
     - The key derives from apid's session key. The data plane (step 4) verifies tokens without reading Postgres, and treats a session it no longer routes as revoked.
   - **Limits.** §7's table is in `pkg/api/limits.go`, with `SandboxIdleFreezeSec`, `SandboxIdleExpirySec` and `SandboxTokenTTLSec`.
3. guest-init sandbox server mode: exec (with `run_as` and background handles), pseudo-terminal, files, archives. The netns anti-spoofing rule and a guest kernel that cannot load modules come with the root metal tests (§10).

   **Built, part one (2026-11-02), on a local branch: the guest server.**
   - **Protocol** (`pkg/sandboxproto`), vsock port 1035. Each operation is one connection: a request frame, then any data, stdin, resize or kill frames from the host, and stream or data frames back, ending in one result or error frame. The framing is agentrunproto's.
     - The operations are `exec`, `exec_list`, `exec_output` and `exec_kill`; `file_read`, `file_write`, `file_list`, `file_stat`, `file_remove`, `file_mkdir` and `file_rename`; and `archive_create` and `archive_extract`.
     - The guest refuses an invalid request before acting on it. Its error codes are stable (`not_found`, `outside_workspace`, `too_large`, …), and it never reports internal details.
   - **Mode.** `/etc/faas/sandbox.json` (kind `sandbox`) selects sandbox mode, alone. guest-init brings up loopback and the relay and prepares `/workspace` and `/home/app` (uid 1000). It listens before the resume hook, so a preset snapshot can be captured there, and holds the restore-reseed contract like an agent run. It then serves up to 64 operations at once until vmmd stops the VM.
   - **Commands** run as `app` or, with `run_as: root`, as root, in their own process group, or their own session on a terminal.
     - A foreground command streams its output within `max_output_bytes`, shared by both streams, and drains the rest.
     - Its timeout sends SIGTERM, then SIGKILL after 2 s. A kill frame, or the host closing the connection, kills it.
     - A grandchild that keeps its output open delays the result by at most 500 ms.
     - A background command returns a handle and keeps the latest `max_output_bytes` of each stream, read by offset. At most 32 are kept, and finished ones are forgotten first.
     - As PID 1, guest-init runs a single `wait4(-1)` reaper that hands each command its own exit.
   - **Files and archives** resolve every path inside the workspace with `os.Root`, so an absolute path elsewhere, `..`, or a link that leaves the workspace is `outside_workspace`.
     - What the files API and extraction create is owned by `app`.
     - Archives are tar.gz, with links kept as links, never followed. Creation is capped in bytes, and extraction in entries and unpacked bytes.
   - **Not yet:** the anti-spoofing rule, the module-less guest kernel and the §10 root metal tests (part two), and the workspace scratch drive, which vmmd attaches in step 4.

   **Built, part two (2026-11-05), on a local branch.**
   - **No kernel code.** Every guest kernel build turns off `CONFIG_MODULES` and `CONFIG_KEXEC_FILE` (`CONFIG_KEXEC` is already off): the release build (`scripts/build-firecracker-kernel.sh`), which checks again after `olddefconfig` and refuses a config that builds anything as a module, as well as the Ansible fallback build and the packer build.
     - Firecracker's pinned 6.18 config builds nothing as a module, so no driver is lost.
     - The kernel digest changes, so runtime snapshots taken on the old kernel go stale. Apps re-snapshot lazily through cold boot (ADR-005).
   - **Anti-spoofing.** With `netns.Config.GuestMAC` set, a `netdev` table (`faas_guard`) hooks `tap0`'s ingress at priority -500, ahead of NAT and every other rule. It accepts only IPv4 from `10.0.0.2` and ARP for `10.0.0.2`, both from that MAC, and counts and drops everything else, IPv6 included (`faas_guest_spoof`).
     - Sandbox sessions in `registries` mode set the fixed MAC `netns.SandboxGuestMAC` (`06:00:0a:00:00:02`, identical across VMs like the guest IP, ADR-009), which Firecracker gives the guest as `guest_mac`.
     - `none` sessions have no network interface.
     - App, job and agent-run VMs get no guard yet. Their MAC is Firecracker's random choice, and restored snapshots carry whichever one they booted with, so a MAC rule would cut off every existing snapshot. Fixing their MAC first is a follow-up.
   - **Metal test** (`TestMetalSandboxRootStaysInsideTheVM`, run by `scripts/experiments/agent-runs/metal/run_sandbox_root_metal.sh` against a kernel built by the release script for the host's Firecracker). As root, a registries session:
     - finds `init_module`, `finit_module` and `kexec_file_load` answering `ENOSYS`;
     - has a spoofed source address and a spoofed MAC dropped and counted by the guard, while its ordinary traffic passes;
     - cannot connect to metadata, the bridge's SSH, RFC1918 or mail ports, even on an address the registries resolver answered, which stays open on 443;
     - stays under `memory.max` when it allocates past its RAM: the guest's own OOM killer stops the process at a peak just under the limit, and the session keeps running.
     - Passed on gregale-internal-test-1 (Firecracker 1.7.0, Linux 6.1.134 built by the release script with these edits; leakcheck clean before and after). Running ordinary traffic through it shows no false drops.
   - **Not yet:**
     - `sudo apk add` in registries mode needs gatewayd-internal's registries resolver, so it belongs to the full-stack native lane;
     - the agent base has no `sudo` for `app` yet (§10), so `run_as: root` is the way to get root.
4. vmmd `SandboxSession` and gateway routing, on the ADR-1037 transport.

   **Built (2026-11-02), on a local branch.** Nothing is reachable until step 5 boots sessions.
   - **vmmd `SandboxCall`** is a bidi stream that carries one sandboxproto operation, matching the guest's one connection per operation. §4 named it `SandboxSession`.
     - The first message names the instance and the session and carries the request. vmmd validates the request before it dials.
     - vmmd marks the instance active for the whole call, so ADR-1151 cannot quiet-pause it mid-operation, and resumes it if it is quiet-paused. A frozen instance is `Unavailable`.
     - It dials vsock port 1035 only on a live instance whose fcvm `SandboxID` matches, which step 5's boot path sets. Anything else is `NotFound`.
     - It relays the host's data, data-end, stdin, resize and kill frames to the guest and drops every other frame type. It relays the guest's frames back until the result or error. It parses nothing in them.
     - `SandboxCall` is a local data method (ADR-20261030230157). A certificate caller must be gatewayd-internal, by the caller policy of bug hunt H17.
   - **Placement rides in the token.** A read of a placed session mints a token with its node and instance, plus the plan's command timeout and output caps. Before placement the token routes nowhere: the data plane answers `409 sandbox_not_ready` with `Retry-After: 1`, and the caller reads the session again.
   - **Routes** (`pkg/gateway/sandboxdata`) are served by gatewayd-internal, ahead of the apid proxy. gatewayd-public treats `api.IsSandboxDataPath` (`exec`, `files`, `stdin` and `workspace` under a session; not `ports`) as compute-owned. The routes:
     - `POST …/exec` takes a command line, which runs under `/bin/sh -c`, or an argument vector. It answers JSON, or SSE (`stdout`, `stderr`, then `result` or `error`) under `Accept: text/event-stream`, or `202 {handle}` for a background command. The timeout defaults to 60 s within the plan cap. A value above a cap is refused with `agent_limit`, carrying the limit and the observed value. `tty` is `sandbox_unsupported` until the interactive attach.
     - `GET …/exec`, `GET`/`DELETE …/exec/{handle}`.
     - `GET`/`PUT …/files?path=`, `…/files:list`, `…/files:stat`, and `POST …/files:remove`, `:mkdir`, `:rename`.
   - **Credentials.** The token is the only credential. The gateway verifies it with apid's public key (see step 5's token keys), so no call reads Postgres. A missing, forged, expired or other-session token answers `404`, like an absent route.
   - **Problem codes.** Guest refusals get stable codes of their own: `sandbox_entry_not_found`, `sandbox_path_exists`, `sandbox_path_outside_workspace`, `sandbox_not_a_directory`, `sandbox_is_a_directory`, `sandbox_directory_not_empty`, `sandbox_too_large`, `sandbox_busy` and `sandbox_command_start_failed`. A session no longer on its node is `sandbox_not_found`.
   - **Not yet:**
     - the terminal (`stdin`, the WebSocket), which comes with the interactive attach; the archive routes came with step 7;
     - the workspace scratch drive and setting `SandboxID`, which come with step 5's boot path;
     - a token check in vmmd as well, like attach's, which comes with step 5: the boot path can hand vmmd a key derived for that session alone;
     - the memory integral, which comes with step 11;
     - OpenAPI and SDK entries, which come with step 9.
5. agentd session lifecycle (idle timers, freezing, expiry) and schedd placement from preset snapshots, with teardown.

   **Built, part one (2026-11-04), on a local branch.** The session lives for exactly one stream: agentd → schedd `RunSandbox` → vmmd `RunSandbox`. A restart of agentd, schedd or vmmd ends the session's VM, and agentd's sweep fails the session with `lease_lost`; ephemeral sessions accept that. Three switches gate it, each off by default: `FAAS_AGENTD_SANDBOXES`, `FAAS_SCHEDD_SANDBOXES` (next to `FAAS_SCHEDD_AGENT_RUNS`) and vmmd's agent-runs switch.
   - **agentd** (`pkg/agentd/sandboxes.go`) has its own slots, 64 by default.
     - It claims a queued session under a lease and opens a stream. On placement it sends proceed. On boot it records `node_id` and `instance_id` and marks the session `ready`, so the next read mints a token that reaches the VM.
     - Each lease renewal (every third of the TTL) also writes the last call vmmd reported to `last_activity_at`, so calls cost no write each. The renewal returns apid's close request and changes to the timeouts.
     - Timers:
       - after `SandboxIdleFreezeSec` without calls it asks for a freeze (state `frozen`);
       - after the session's idle timeout it closes the session as `expired`, outcome `idle`;
       - at the session's end it closes it as `expired`, outcome `lifetime`;
       - on a close request it closes it as `closed`.
     - Failures:
       - a refusal or broken transport before boot queues the session again;
       - a plan refusal fails it as `rejected`;
       - a VM lost after boot fails it as `vm_lost`.
     - The sweep queues expired `starting` sessions again, fails expired live ones with `lease_lost`, and expires queued sessions past their end.
   - **Shared limit.** `AccountAgentLoad` counts live sessions (`starting`, `ready`, `frozen`) with live runs, so the two share `AgentRunConcurrentPerAccount` (§7).
   - **schedd** (`pkg/sched/sandbox_runs.go`) admits a session the way `AgentRunner` admits runs: `KindAgentRun` on the agent pool, instance `sandbox-<id>`, with the plan's agent-run vCPU. It holds the reservation for the call, relays freeze and close, and keeps no session state.
   - **vmmd** (`pkg/vmmdgrpc/sandbox_run.go`; schedd's certificate only):
     - `fcvm.WakeSandbox` cold-boots the agent base with the new platform sandbox layer (`base/sandbox-layer-v1-<arch>.ext4`, holding `/etc/faas/sandbox.json`, published by imaged beside the agent layer). It sets `Instance.SandboxID` and returns once the guest's sandbox port answers.
     - It reports calls at most once a second (`calls_in_flight`, `last_call_ended`). It freezes in place without reclaim when asked; a call in flight refuses the freeze.
     - It destroys the VM on close and whenever the stream ends.
     - `SandboxCall` thaws a frozen session in place, waiting out a freeze in progress, and reports `thawed`. A call never waits on schedd or the database.
     - Session VMs are carved out of the guards that keep execution-only VMs from being quiet-paused (§5, 2 s between calls) and frozen; `ExecuteExecution` and `RunAgent` refuse them.
   - **Token keys (owner decision, 2026-11-04).** ADR-20261102191500 took the session key off every gateway, and compute nodes had held per-host session keys anyway, so step 4's HMAC tokens would not have verified on another node.
     - Tokens are now Ed25519-signed (format `sbx2.<key id>.<payload>.<signature>`). apid derives the private key from its session key.
     - Gateways fetch the public keys over the Auth gRPC (`SandboxTokenKeys`, on the channel `VerifySession` uses). They refetch every 5 minutes, and every 5 s until the first answer.
     - A gateway verifies tokens and cannot mint one. Until its keys arrive it refuses every token like an absent route.
     - Rotation follows apid's session key: a token signed under a retired key fails at the next refetch.
   - **Not yet:**
     - ~~preset snapshots for sessions (every session cold-boots)~~: built with template warm snapshots (step 10, part five);
     - the workspace scratch drive (the workspace lives on drive1's 512 MB);
     - memory reclaim on freeze;
     - backoff for a session that fails to boot again and again;
     - metrics.
6. Network modes `none` and `registries`.
   - **As built (local branch, no PR yet).** The mode rides the session from its row to the VM: agentd's claim → schedd `SandboxRunStart.network_mode` → vmmd `RunSandboxStart.network_mode` → `fcvm.SandboxWakeRequest.Network`. An empty mode, from a daemon of the previous release, is `none`. `web` is refused at schedd, vmmd and fcvm.
   - **`none`** boots without a network, as step 5 did.
   - **`registries`** uses a DNS-pinned allowlist on ADR-373's DNS-gated egress, not an ADR-031 CIDR list: registry CDNs change addresses too often for a CIDR list.
     - The VM gets the ordinary netns and `tap0` and stays execution-only. `Lease.AgentSized` keeps agent-run CPU sizing for it.
     - Egress: the DNS gate, new TCP on 443 only, and the plan's connection rates. The VM reaches no bridge service port, private service address, managed-database route or SMTP.
     - The guest's DNS is pinned to a third bridge resolver on port 5356 (`gateway.SandboxRegistriesDNSPort`, `netns.SandboxRegistriesDNSPort`). It forwards one A or AAAA question for a name in `api.SandboxRegistryDomains` or a subdomain of one, and answers REFUSED to every other query, private service names included.
     - Only answers to allowed names reach the egress gate. vmmd checks the name again (`AllowResolvedEgressRequest.name`): a registries VM admits no address for any other name, or for an empty one.
     - vmmd refuses to boot a registries session when DNS-gated egress is switched off.
   - **Domains.** `api.SandboxRegistryDomains` covers:
     - npm and Yarn;
     - PyPI;
     - crates.io;
     - the Go module proxy and checksum database;
     - GitHub, including `githubusercontent.com`;
     - the Alpine, Debian and Ubuntu mirrors over HTTPS.
   - **Known gaps:**
     - An allowed name on a shared CDN opens that CDN address to every host it serves, so domain fronting through it is possible.
     - Mirrors that serve only port 80 are unreachable. This affects many apt mirrors, so `apt` needs HTTPS sources.
     - Private registries and custom git hosts are not yet configurable.
7. Workspace persist and hydrate.
   - **As built (local branch, no PR yet).** Two switches gate it: agentd's `FAAS_AGENTD_SANDBOX_WORKSPACES` (next to `FAAS_AGENTD_SANDBOXES`), and vmmd's artifact store, which every node has.
   - **`POST …/workspace:hydrate?path=`** (gatewayd-internal) unpacks the uploaded tar.gz into the workspace through `SandboxCall` `archive_extract`. The unpacked size is capped at the plan's `SandboxWorkspaceMaxMB`, which the session token now carries (`wb`), and the entry count at `SandboxArchiveEntriesMax`. A token minted before the cap was added gets `409 sandbox_not_ready`, so the caller reads the session again.
   - **`POST …/workspace:persist`** (body `{path?}`) reaches the node's vmmd through `PersistSandboxWorkspace`, a gateway-only local data method. Before writing, vmmd asks agentd for a reservation over the session's `RunSandbox` stream: `persist_requested` → `persist_grant`. schedd relays each message both ways and keeps no state.
     - **Reservation.** agentd holds the account's agent lock and inserts a pending `sandbox_workspace_snapshots` row (migration `20261110153935557`, expand). The reservation is whatever is left of `SandboxSnapshotBytesPerAccount`, at most `SandboxWorkspaceMaxMB`. Pending rows count their full reservation against the quota, and ready rows count their actual size. A full quota is refused as `agent_limit`.
     - **Write.** vmmd streams the guest's `archive_create` straight into the artifact store at `agents/workspaces/<account>/<snapshot>.tar.gz`, then reports `persist_done`. agentd marks the row ready with its size and digest, and only for a reservation this session's stream made. agentd answers `persist_recorded`.
     - **Answer.** vmmd answers `{snapshot_id, bytes, sha256, entries, expires_at}` only after the snapshot is recorded, so every ID a caller holds can be used. If the write fails or the snapshot is not recorded, vmmd deletes the archive and tells agentd to drop the reservation.
   - **Starting from a snapshot.** `POST /v1/sandboxes` takes `snapshot_id`, which must be the account's own, ready, unexpired and not being deleted; otherwise it is `404 sandbox_snapshot_not_found`.
     - The claim carries the archive's key (`workspace_key` on both starts). vmmd unpacks it before `booted`.
     - A snapshot deleted after the create, or an archive that cannot be unpacked, fails the session as `workspace_unavailable`. vmmd returns `sandbox_workspace_unavailable`, which schedd maps to `DataLoss`. The session is never started empty and never requeued. vmmd also refuses a key outside the session account's prefix.
   - **Snapshots API** (apid, agents capability): `GET /v1/sandbox-snapshots` (newest first, paged with `before`), `GET` and `DELETE /v1/sandbox-snapshots/{id}`. A delete sets `delete_requested_at`, and the snapshot is hidden and unusable from then on.
   - **Retention.** agentd's sweep deletes the archive first, then the row, for three kinds of snapshot: those past `SandboxSnapshotRetentionDays`, those apid was asked to delete, and pending ones older than `SandboxSnapshotPendingSec` (1 h). The pending timeout outlasts `SandboxPersistTimeoutSec` (30 min). A delete that fails keeps the row, so the next sweep retries it.
   - **Known gaps:**
     - vmmd's artifact store is the node's read-through cache, so a persisted or restored archive also lands in that node's cache until LRU eviction.
     - A 20 GB archive streams for minutes; gatewayd-internal's response timeouts apply to persist and hydrate just as they do to long `exec` calls.
     - Snapshot bytes are not billed yet (step 11).
     - `sandbox_workspace_archive_bytes` is not emitted yet.
8. Exposed ports and preview URLs: the preview domain, the vsock relay, the gateway host route, tokens, and rate limits (§11).
   - **As built (local branch, no PR yet).** The owner chose three things on 2026-11-05: the preview domain is configuration, the transport is a vsock relay, and routing is a cached lookup.
   - **Domain.** `FAAS_SANDBOX_PREVIEW_DOMAIN` (apid and gatewayd-internal). While it is unset, previews are off: the port routes answer `503 sandbox_previews_off`, and gatewayd-internal serves no preview hosts. The domain's name is still open question 3. So are its wildcard DNS, the wildcard certificate at the upstream TLS edge, and its Public Suffix List entry.
   - **Store.** `sandbox_ports(sandbox_id, account_id, port, visibility)` (migration `20261110153935586`, expand). apid is its only writer. A port is added under the live session's row lock, within the plan's `SandboxExposedPortsMax`, and exposing an exposed port again changes its visibility. Rows go when the session row goes.
   - **API** (apid, agents capability): `POST /v1/sandboxes/{id}/ports {port, visibility}`, `GET …/ports`, `GET` and `DELETE …/ports/{port}`. A port's view carries `url`, and for a private port of a live session a preview token (`SandboxPreviewTokenTTLSec`, 1 h, at most the session's lifetime). The list carries no tokens. Ports in `SandboxReservedPorts` are refused. `public` needs `SandboxPublicPreview`; without it the request gets `402 plan_feature_gated`. `ports` at create is not built.
   - **Tokens.** A preview token is its own format (`sbxp1`), signed by the same apid Ed25519 key, and grants one port of one session. A data-plane verifier refuses one by its prefix, old gateways included, and the preview handler refuses data tokens.
   - **Guest.** `port_connect {port}` dials `127.0.0.1`, then `::1`, within 2 s. A port nothing listens on is `port_closed`, which the gateway answers as `502 sandbox_port_closed`. Each connection holds one of guest-init's operation slots.
   - **Gateway** (`pkg/gateway/sandboxpreview`, carved out ahead of the apid proxy by host):
     - The route (the session's account, plan, state, node and instance, and its ports) is read from Postgres once per `RouteCacheTTL` (5 s), misses included, with one read per session in flight. A removed port or an ended session stops answering within 5 s.
     - Unknown hosts, unexposed ports, ended sessions and missing or wrong tokens answer 404. `?gregale_preview_token=` sets the host-only `__Host-gregale_preview` cookie and 303s to the URL without it.
     - The preview cookie, a bearer preview token and `X-Faas-*`/`X-Gregale-*` headers are stripped. The app's own `Authorization` and cookies pass through. The `Host` header stays the preview host, with `X-Forwarded-Proto: https`.
     - Each request is its own guest connection, without keep-alive, so no idle pooled connection keeps the session busy. WebSocket and other upgrades are spliced over the same call.
     - Per session: `SandboxPreviewRequestsPerSec` (burst 2×) and `SandboxPreviewConnsPerSession` (32) open connections.
   - **Wake.** vmmd's `SandboxCall` already thaws a frozen session and counts the call as activity while it is open, so an open WebSocket keeps the session from idling.
   - **Known gaps:**
     - The path through `gatewayd-public` and the upstream TLS edge for a preview domain is not yet tested end to end.
     - The metal tests listed in §11 are not run yet.
     - `sandbox_ports_open` and `sandbox_preview_requests_total` are not emitted yet.
     - The `GregaleSandboxClient` and the shim (steps 9 and 10) do not call the port routes yet.
9. `GregaleSandboxClient` (with `resolveExposedPort`) and the PR-triage example.
   - **As built (local branch, no PR yet).** `@gregale/agents-sandbox` lives in its own private package, `sdk/agents-sandbox`, not inside `sdk/node`, so the Node SDK takes no `@openai/agents` peer dependency. It implements `SandboxClient` and `SandboxSession` from `@openai/agents-core/sandbox` 0.21.0, which is pinned as the peer dependency. Its tests run under `make test-agents-sandbox`, which no CI job runs yet.
   - **Interface.** The Agents SDK's remote-session base class (`@openai/agents-extensions`, used by E2B, Modal and the others) is not exported, so the session implements the public interface directly. The client leaves `supportsDefaultOptions` unset, like the other remote providers: the SDK then passes no default `local` snapshot, and any other core snapshot is refused in favour of `persistSnapshot()` and `snapshotId`.
   - **Commands.** The shell tool's `execCommand` starts a background command and polls its output for up to `yieldTimeMs` (10 s by default). A command still running keeps an SDK session ID, and `writeStdin` with empty `chars` polls it. `supportsPty()` answers true so the SDK offers `write_stdin` for polling, but input is refused, because Gregale has no stdin route yet. `tty` is refused, and so is `runAs` other than `app` or `root`.
   - **Manifest.** `file`, `dir`, `local_file` and `local_dir` entries go up in one `workspace:hydrate` tar. A `git_repo` is fetched in the guest (`git fetch --depth 1 origin <ref>`, so `refs/pull/N/head` works) and needs a network mode that reaches its host. The manifest's environment is sent with every command and is never serialized. Mounts, users, groups, path grants, entry groups and a root other than `/workspace` fail before any session is created.
   - **State and tokens.** `serializeSessionState` is `{sandboxId, apiBase}`. `deserializeSessionState` and `resume` refuse a state for another API origin, so a forged state cannot send the API key elsewhere. The session token is re-minted from the API key near expiry, and once when the data plane rejects it. A private port's preview token is returned in the endpoint's `query` and is never stored in `state.exposedPorts`.
   - **Workspace.** `persistWorkspace` builds a tar in the guest and downloads it through `…/files`; `hydrateWorkspace` gzips the tar and uploads it. `persistSnapshot()` is Gregale's server-side `workspace:persist`.
   - **Tested.** There are unit tests against a fake API, a real `SandboxAgent` run through the SDK's runner with `ScriptedModel`, and the PR-triage example (`examples/pr-triage.ts`) run the same way.
   - **Not tested against a deployed Gregale**, where sandbox sessions are switched off. There is no journey for it yet. A journey needs the switches on in a test environment.
10. The E2B-compatible shim, template import, and a migration guide with the coverage table (§12, §13).
   - **As built, part one (local branch, no PR yet).** `@gregale/sandbox-e2b` (TypeScript, private) lives in `sdk/sandbox-e2b` and has no runtime dependencies. Its tests run under `make test-sandbox-e2b`, which no CI job runs yet.
     - It is written from the E2B SDK's public interface (`e2b` 2.63.0: method names and option shapes) and copies no E2B code. The error classes keep E2B's names, so existing `catch` code still matches.
     - **Sandbox:** `create` (template = preset, `base` = the default), `connect`, `list`, `kill`, `getInfo`, `setTimeout`, `isRunning` and `createSnapshot` (`workspace:persist`).
     - **Commands:** `run` in the foreground (JSON, or SSE when `onStdout` or `onStderr` is set, with `CommandExitError` on a non-zero exit) and in the background (a handle polled for output). `list`, `kill` and `connect` work with numeric pids, which the client maps to Gregale handles.
     - **Files:** the files API inside `/workspace`. Reads, writes, `exists`, `makeDir` and `remove` on paths outside it go through commands and are capped at 256 KB.
     - **Ports:** `getHost(port)` is synchronous in E2B, so it answers only for ports exposed at `create` (`ports`) or with `await exposePort(port)`, and `previewToken(port)` returns a private port's token.
     - **Refused with `UnsupportedError`,** naming the call and the alternative: `pause` and `betaPause`, `pty`, stdin, `watchDir`, MCP and custom templates. `envs` go with every command, because create takes no session env yet. `allowInternetAccess: true` asks for `web`, which apid still refuses.
   - **Migration guide:** `docs/sandbox-migration.md`, holding the coverage table, the E2B call mapping, the Agents SDK move, workspaces and network modes. It is not in `customer-pages.json` until launch.
   - **Not built yet:**
     - ~~The Python shim `gregale-sandbox-e2b`~~: built in part four.
     - ~~Template import~~: built in part three.
   - **As built, part two: seller-image sessions (local branch, no PR yet).** `POST /v1/sandboxes {"image": "<ref>"}` boots a customer OCI image in place of a preset.
     - **Amends §2:** the image is cached in ADR-1161's image-run cache, not a separate per-account seller-image store. apid gets or creates the image's runner job (`imagerun.Handler.EnsureSessionImage`), so sessions and image runs share one cache, one `ImageRunCachedImages` limit and one pull. Image sessions need image runs switched on; otherwise `image` is `sandbox_unsupported`.
     - **Any OCI image** boots as a full rootfs (imaged's job materialization) with guest-init's sandbox server as PID1, like a job image. The session records preset `image` and defaults to `SandboxImageDefaultRAMMB` (1024 MB). `preset` and `image` together are a validation error.
     - **A cold image queues.** Create answers 201 `queued`. agentd claims the session only once the runner job's image is ready. A queued session whose image failed or whose job was deleted fails with outcome `image_unavailable`, carrying the pull error (`FailImageUnavailableSandboxSessions`, run by the lease sweep).
     - **Eviction:** the image-run LRU skips a runner job with a queued, starting, ready or frozen session (`sandbox_sessions.image_job_id`), so a full cache answers `image_run_cache_full` instead of pulling a live session's image away.
     - **Data path:** agentd passes the image's storage key as `image_key` (schedd `SandboxRunStart` 9, vmmd `RunSandboxStart` 13). vmmd accepts only a job image key (`storagekey.IsJobLayer`), boots it as drive1 in place of the platform sandbox layer, and stages `/etc/faas/sandbox.json` on the VM's private copy of drive1, as job VMs get `job.json`. The cached image is never changed.
     - **Migration** `20261110153935627_sandbox_session_images` (expand): `image_ref` and `image_job_id` on `sandbox_sessions`.
     - **Metal test:** `TestMetalSandboxFromASellerImage` (pkg/fcvm, run by `scripts/experiments/agent-runs/metal/run_sandbox_root_metal.sh`) boots a Debian image packed as imaged publishes a job image over the Alpine agent base, and checks the image's root, the staged marker, uid 1000, a writable `/workspace`, root, and that the cached image is unchanged afterwards. It passed on internal-test-1 on 2026-11-09 with leakcheck clean.
     - **Not built:** registry credentials for private images on create (image runs take `registry_auth`; sessions use the runner job's stored credential, if any), and `pull: always`.
     - The pricing calculator and shadow mode from §13.
   - **As built, part three: templates (local branch, no PR yet; slices A–E).** `gregale sandbox template publish [dir]` builds a template from `e2b.Dockerfile` and `e2b.toml`; sessions start from it with `{"template": "<name>"}`.
     - **A hidden build app per template.** `PUT /v1/sandbox-templates/{name}` (`start_cmd`, `ready_cmd`, `ready_timeout_s`: default 60, at most `SandboxTemplateReadyTimeoutSecMax` 600) creates the template and its app: internal, execution mode `job`, slug `sbt-` plus 12 hex (reserved, `api.IsSandboxTemplateAppSlug`), marked `apps.sandbox_template`. The app counts in `SandboxTemplatesMax` (Free 0, Hobby 3, Pro 20, Scale 100) instead of `DeployedApps` and is left out of app listings. `GET` lists or reads templates with their newest build; `DELETE` deletes the app first, so a failure leaves the template whole. Migrations `20261110153935666_sandbox_templates` and `20261110153935695_sandbox_session_templates` (expand).
     - **Builds are deployments of that app** through the normal deploy path, in a builder microVM (ADR-003). A job-mode app skips the snapshot-prime boot. imaged publishes a template's build as a full rootfs (`publishSandboxTemplateRootfs`); an image with no command gets `/bin/sh`.
     - **Sessions** boot the newest `live` build's rootfs (`apps/sbt-…/<uuid>.ext4`, `storagekey.IsSandboxTemplateLayer`, which vmmd accepts beside job layers) like a seller image, recording preset `template`, `image_ref` `template:<name>` and `template_id`. Create answers `sandbox_template_not_found` (404) or `sandbox_template_not_ready` (409); a template deleted before the session starts fails it as `image_unavailable`.
     - **Amends §12: the start command runs at boot, not before a warm snapshot.** agentd passes `start_cmd`, `ready_cmd` and `ready_timeout_ms` (schedd `SandboxRunStart` 10–12, vmmd `RunSandboxStart` 14–16). fcvm stages `sandbox.json` version 2 with them; guest-init runs the start command in the background as uid 1000 (output in `/tmp/gregale-start.log`), polls the ready command each second (10 s per attempt), and listens only once it passes. A ready command that never passes powers the VM off, so the wake fails. Template warm snapshots can follow with ADR-20261027090015.
     - **CLI:** `gregale sandbox template publish|list|get|rm` (`sandbox-templates`; `build` also works). `publish` reads `template_name`, `dockerfile`, `start_cmd` and `ready_cmd` from `e2b.toml` (flags override; `cpu_count` and `memory_mb` are ignored), saves the template, packs the directory with the chosen Dockerfile at the archive root as `Dockerfile`, deploys it to the template's app, and waits for the build.
     - **Shim:** `Sandbox.create('<name>')` with a name that is not a preset or `base` creates the session from that template; `getInfo().templateId` is the template's name.
     - **Tested:** store, apid handler, imaged, agentd, vmmdgrpc, fcvm and CLI unit tests; guest-init tests and the metal tests `TestMetalSandboxFromASellerImage` and `TestMetalSandboxTemplateCommands` passed on internal-test-1 on 2026-11-10 with leakcheck clean. **Not tested** end to end against a deployed Gregale (build to session); that needs sandbox sessions switched on in a test environment.
     - **Not built:** ~~template warm snapshots~~: built in part five.
   - **As built, part five: warm session snapshots (local branch, no PR yet).** Presets and templates restore from a warm snapshot instead of cold-booting.
     - **Capture point.** A session VM is captured once guest-init's sandbox server and resume hook are up (console stage `sandbox-ready`), before any session identity, call, file or template command enters it. So the ADR-171 contract and invariant 5 hold as for agent-run presets. `fcvm.CaptureSandboxSnapshot` couples drive1 (v2 keys).
     - **Templates are captured before their start command** (owner decision, 2026-11-10). The alternative, a capture after the ready command, would have shared the start process's memory and userspace RNG state between sessions. A restored template session gets its commands by the new host-only sandboxproto op `template_start`. guest-init runs them once and answers when the ready command passes; vmmd's `SandboxCall` refuses the op from callers. A cold boot still runs them from `sandbox.json` version 2.
     - **Restore.** `RunSandboxStart.snapshot` (17) reaches `fcvm.WakeSandbox`. vmmd restores only a sandbox capture of the session's own kind: `snap/sandbox-preset/…` for a preset session, `snap/sandbox-template-<account>/…` for an image session (`storagekey.IsSandboxSnapshotKey`). So no account restores another's template. The snapshot is networked for `registries` and networkless for `none`. A failed restore cold-boots (ADR-005).
     - **schedd** (`pkg/sched/sandbox_snapshots.go`, behind `FAAS_SCHEDD_SANDBOX_SNAPSHOTS`, default off) keeps one slot per node, base, drive, machine shape and network mode. A template's slot is its build app (`storagekey.SandboxImageIdentity`) and its account. After a shape's first cold-booted session ends cleanly, schedd asks the node's vmmd to capture it (`CaptureSandboxSnapshot`, schedd's certificate only), under that session's reservation.
     - **Staleness.** A session restores a slot only when it holds the session's exact drive1, so a rebuilt template cold-boots and recaptures into the same keys. A session that cold-boots despite a snapshot drops it. Slots are bounded by `SandboxWarmSnapshotsMax` (512) and are per schedd process, like agent-run presets.
     - **Metal** (internal-test-1, FC 1.7.0 / 6.1.134, leakcheck clean): `TestMetalSandboxWarmSnapshot`. Two preset restores are ready in 590–664 ms with distinct RNG streams, stepped clocks and an empty workspace. A template restore runs its start command once, after the restore. Two `registries` restores of one capture each get their own netns, a working network on 10.0.0.2, and the egress rules: 443 only to resolved addresses, with mail, metadata and private ranges closed. They are ready in 539–605 ms, and a `none` session refuses the networked snapshot.
     - **Restore under host I/O pressure.** One run on the shared test-1 timed out the 5 s resume-hook budget on a restore and fell back to a cold boot, as ADR-005 intends. The rerun passed.
     - **A deleted template's snapshots go** (2026-11-11). Every `SandboxTemplateSnapshotSweepSec` (300 s) schedd reads each template slot's build app. A slot whose app is deleted, or is no longer that account's template app, is dropped, and its objects are deleted on the node through vmmd's `DeleteWarmSnapshot` (schedd's certificate). A failed delete is logged; the slot is dropped anyway, since nothing restores it again. Slots are per schedd process, so a template deleted while schedd was down leaves its objects behind until an operator clears them.
     - **One capture per build.** A session that cold-boots while another session's capture of the same build runs finds that capture in the slot afterwards and does not capture again.
     - **Not built or not tested:**
       - `preset_residency_mb` for session snapshots (ADR-20261027090015 §1).
   - **As built, part four: the Python shim (local branch, no PR yet).** `gregale-sandbox-e2b` (import `gregale_sandbox_e2b`, private, Python 3.12+) lives in `sdk/sandbox-e2b-python` and uses only the standard library. Its tests run under `make test-sandbox-e2b-python`, which no CI job runs yet.
     - It mirrors the TypeScript shim call for call, with the E2B Python SDK's names and arguments: `Sandbox(...)` and `Sandbox.create(...)`, `connect`, `list` (a paginator with `has_next` and `next_items()`), and `kill`, `get_info` and `set_timeout` on the class with an ID or on an instance. A `with` block closes the session. Times are in seconds, as in E2B's Python SDK. Exceptions keep E2B's Python names (`SandboxException`, `CommandExitException`, `NotFoundException`, `TimeoutException` and the rest), plus `UnsupportedException`.
     - **Synchronous only:** there is no `AsyncSandbox`. A background command's `on_stdout`/`on_stderr` run while the caller waits on the handle or iterates it, because the client starts no threads.
     - **Transport:** `urllib` with redirects refused, so the bearer never follows a redirect; tests inject a fake transport, and one test drives the real one against a local HTTP server.
11. Billing, with ADR-20261027090011.
   - **As built, part one: measurement, not billed (local branch, no PR yet).** Each session VM is measured by ADR-20261027090011's Measurement for its whole life, from boot to close.
     - **What is measured.** vmmd's `MeterSandbox` (pkg/fcvm) samples private memory (`anon` + `shmem`) every `AgentRunUsageSampleMillis`, as for a run VM, through the same meter. Quiet-paused and frozen seconds are paused time, which is never billable (§5, D15). A freeze first ends any quiet pause, so the two never overlap. fcvm keeps a frozen ledger for this (`Instance.FrozenTotal`, `Manager.FrozenTime`). The record is method 1, with `frozen_ms`, the frozen part of `paused_ms`.
     - **Reporting.** A session lives for hours, so vmmd sends a cumulative `usage` frame on `RunSandbox` every `SandboxUsageReportSec` (60 s), and a last one marked `final` before the stream ends on close. A vmmd that dies loses at most one interval. schedd relays the frame (`SandboxRunEvent.usage`), reusing `AgentRunUsage` with the new fields 11 (`frozen_ms`) and 12 (`final`).
     - **Record.** agentd keeps the latest record in `sandbox_session_usage` (migration `20261110153935721`, expand), fenced on the session's lease while it is `ready` or `frozen`. An older record never replaces a newer one, and nothing replaces the final one.
     - **Metrics:** `sandbox_session_billed_gb_seconds_total` (the billable measure; nothing bills it yet) and `sandbox_session_paused_seconds_total`, on the agent runs dashboard.
     - **Tested:** fcvm meter and frozen ledger, the vmmd stream (periodic and final frames), the schedd relay at both ends, the store's fence and ordering on scratch PG, and agentd recording the final record at close. **Not tested on metal.** The metal tests owed by ADR-20261027090011 cover the sampling itself.
   - **Not built yet:**
     - **meterd's `agent` meter kind and the conversion `k`.** These are shared with agent runs (ADR-20261027090011 rollout step 3), and wait on the financial model setting `k` and the floor.
     - **Snapshot storage pricing.** Persisted workspace bytes are counted against `SandboxSnapshotBytesPerAccount` but not billed; that needs a price.
     - Statement and tenant-statement lines.

Long-lived paused sessions (E2B-style memory pause) need their own ADR.

## Open questions

1. Whether sessions share the agent-run concurrency pool (proposed) or get their own.
2. A Python client for the Agents SDK, and adapters for LangGraph and Mastra.
3. The preview domain's name.
4. Sessions that outlive `SandboxIdleExpirySec` with their memory kept, which depend on single-use per-VM snapshots.
5. A stateful Python-kernel preset (Jupyter protocol) for code-interpreter users, and the shim's `runCode`.
6. Docker inside the guest, which needs the guest kernel's cgroup and overlay support for nested containers.
7. Raw TCP ports, for databases and language servers.
