# ADR-20261027090010 · Agent runs: one-shot harness runs and the workflow agent step

- **Status:** proposed
- **Date:** 2026-10-17
- **Amended by ADR-20261027090019** (proposed), §3: a run that names a context gets its repository and dependencies from read-only context drives, not a per-run checkout.
- **Amended by ADR-20261027090016** (proposed), §7: model calls join one per-run capability policy, budget and audit with tools, web and data.
- **Amended by ADR-20261027090015** (proposed), §1 (preset snapshots are captured after a warm-up run, and their shared pages count once per node).
- **Builds on:**
  - ADR-1161 (image runs) and ADR-099 (jobs);
  - ADR-171 (durable intents claimed by schedd under a lease);
  - ADR-081, 262, 263, 487 (workflows) and ADR-488, 574, 575 (customer automations);
  - ADR-100 (triggers);
  - ADR-988, 989, 994, 996 (AI gateway and relay);
  - ADR-226, 238, 240, 244, 245, 247, 248 (platform tenants);
  - ADR-361, 372 (tenant egress);
  - ADR-909 (measured admission) and ADR-1151 (pause tiers).
- **Related:** ADR-20261027090011 (agent runs billed by average used RAM), ADR-20261027090012 (pausing agent runs while they wait on the model), ADR-20261027090013 (sandbox sessions), and internal design paper GA-DP-001 v0.2.
- **Amended by ADR-20261027090014** (when accepted): agentd owns run state (§4), runs are admitted on the agent node pool (§5), and web access follows ADR-20261027090014 §4 (§12).
- **Decision:**
  - Add the **agent run**: one execution of a customer-supplied harness (Codex, OpenCode, Claude Code or any OCI image) in a fresh microVM. The run takes a rendered prompt and an event, writes `output/`, and is destroyed when it exits. Gregale builds no agent loop.
  - Runs are started three ways: by triggers and automations, by a new `agent:` workflow step, and by a seller API.
  - Each run is attributed to a platform tenant.
  - Model calls go through the ADR-989 relay to the AI gateway, so model keys never enter the VM.
  - Results are delivered by Gregale, idempotently, only after the harness exits.
  - Every surface is hidden behind a default-off account allowlist until launch.
- **Why:** The owner positioned Gregale Agents (2026-10-17) as a platform where agent sellers run their own harness for their own customers, starting with short-lived event-driven runs. Almost every piece already exists for apps, jobs and workflows. What is missing is a run kind that ties them together, plus a contract any harness can meet.
- **Consequences:**
  - New tables and a state machine owned by agentd (ADR-20261027090014).
  - A new admission kind ranked below app wakes.
  - An agent mode in guest-init's job supervisor.
  - The relay for run VMs; a delivery worker.
  - An `agent` step executor in the workflow orchestrator.
  - Plan limits in `pkg/api/limits.go`, and new §12 metrics.
  - Sessions, persistent workspaces and reattach stay out of scope.
  - Web-browsing agents stay blocked until the agent egress controls in §12 exist.
- **Rejected alternatives:**
  - A Gregale-built agent SDK or adapter (owner direction: no harness by Gregale).
  - Temporal or NATS as coordinator: ADR-081 already rejected Temporal, and components coordinate through Postgres rows and `pg_notify`.
  - Long-lived sessions first: they need persistent disks, reattach and longer caps.
  - Selling sandboxes rather than finished runs.
  - Putting model keys or short-lived GitHub tokens in the VM: that leaks credentials and, under ADR-1151, blocks freezing.
  - Mounting guest-written filesystems in root vmmd to collect outputs.

## Context

GA-DP-001 v0.1 proposed a Gregale-hosted agent service with its own adapter, Temporal, a single-leader scheduler and fixed 2–8 GiB profiles. The 2026-10-17 review replaced it.

- **The owner's direction:**
  - Gregale runs other people's harnesses;
  - short-lived one-shot runs come first;
  - runs can be chained;
  - creation should feel serverless;
  - sellers bill their own customers.
- **What already exists:**
  - image runs execute any OCI image as a Job task (ADR-1161);
  - workflows run steps with dependencies, fan-out, waits and recovery (ADR-081 and later);
  - automations publish customer-authored workflows (ADR-488);
  - the AI gateway enforces caps, budgets and per-end-user limits (ADR-988 and later);
  - the relay lets a guest reach outboundd without a network route (ADR-989);
  - platform tenants give sellers per-customer identity, budgets and statements (ADR-226 and related).

## Decision

### 1. Scope and gating

- An agent run is one-shot: it starts, works for at most the plan's maximum run duration (§Limits), delivers, and is destroyed.
- **Out of scope:**
  - sessions that resume across runs;
  - persistent workspaces;
  - reattach;
  - GPU work.
- **Gated by default:**
  - Every agent surface belongs to the `agents` product capability (ADR-20261023115741), at the `internal` tier: its platform flag is off for every account until an operator releases it by account rule, and on for everyone at launch.
  - Requests from other accounts are indistinguishable from absent routes. The capability gate answers `httpkit.NoSuchRoute` right after auth, or before it while the kill switch is off. A method mismatch answers the same 404 rather than a revealing 405 while the capability is not on for everyone.
  - Unauthenticated callers meet the 401 of any authenticated route unless the kill switch is off, as for every internal capability.
  - The workflow agent step, trigger agent targets and the `agent_run.finished` webhook event are refused like unknown fields for the same accounts. schedd holds agent-target triggers, and workflow runs with an agent step, while the capability is off for the app's account.
  - Agent routes stay out of the public OpenAPI and the generated SDKs (`routeExclude` in `spec_compliance_test.go` and `cmd/sdk-coverage`) until launch.
  - `gregale agent` commands use the operator CLI audience.
  - The `agent_runs` switches in agentd, schedd and vmmd default off.

### 2. Harnesses and presets

A harness is described by a descriptor stored with the agent version:

```yaml
harness:
  image: ghcr.io/acme/my-agent:1.4    # or built from a repository in a builder VM (ADR-003)
  run:    ["my-agent", "--prompt", "{prompt}"]
  models: { openai: OPENAI_BASE_URL, anthropic: ANTHROPIC_BASE_URL }
  ram_mb: 2048                        # optional; otherwise sized automatically (§5)
```

**Presets.** Gregale publishes two presets: `codex` (`codex exec`) and `opencode` (`opencode run`). Both are open source, and their licenses are to be confirmed against the official repositories before publication.

**Claude Code.** Anthropic permits preinstalling and running Claude Code "in hosted sandboxes or other agent infrastructure", under its Commercial Terms of Service and two conditions (Claude Code legal and compliance page, read 2026-10-18):
1. The binary runs exactly as Anthropic publishes it, and no built-in authentication method is removed, disabled or restricted.
2. No one pays for, resells or intermediates Claude usage on an end user's behalf. Each end user authenticates with their own Anthropic API key, their own Claude plan, or their own credential at a third-party provider (Bedrock, Vertex, Foundry), and is billed directly.

Gregale therefore publishes a third preset, `claude-code` (`claude -p`), with the unmodified published binary and the same snapshot treatment as the other presets. Gregale never ships managed settings that pin providers or disable sign-in methods for it. §7 sets out the credential rules. Sellers can also build their own images; per-account images are cached and bounded by `ImageRunCachedImages`.

**The two-drive rootfs.**
- drive0 is a shared, read-only agent base: Node, Python, git and common tools, plus all three preset harnesses. They total about 0.9 GB and are counted once in base residency, never against a customer's caps. The prototype measured the pinned binaries at Codex 430 MB, Claude Code 280 MB and OpenCode 196 MB, which do not fit as drive1 layers under today's caps.
- drive1 holds only a custom harness's layer, bounded by `AgentImageLayerMaxMB`.
- The full-rootfs path is not used for agents.

**Snapshots.** Presets and per-account images are restored from sanitized snapshots taken after the harness has loaded and before any tenant identity, input or secret enters the VM. This follows the ADR-171 snapshot contract. A missing or stale snapshot falls back to a cold boot of the same image (ADR-005).

**As built in rollout step 9.**
- **Capture point.** guest-init's agent mode listens for its run, starts the resume hook and prints `agent-ready` on the console. vmmd's `CaptureAgentSnapshot` cold-boots a VM with no run, waits for that marker, and snapshots it.
  - The snapshot uses the v2 capture keys, so drive1 is frozen with memory and device state, and a restore gets drive1 exactly as captured.
  - The harness has not run yet. ADR-20261027090015's warm-up run against a stub provider is a later step, so harness files are not yet in the snapshot's page cache.
- **One snapshot serves every preset.** All presets share drive0 and the agent layer, so schedd keys snapshots by node and machine shape (vCPU, memory, CPU) only.
- **The input drive.** Every agent run VM has an input drive of a fixed capacity (`agentrunproto.InputDriveBytes`, 256 MiB plus a header), zeros when the run has no checkout. A captured VM and every VM restored from it therefore agree on its size; a restored guest keeps the capacity it probed at capture.
  - vmmd stages each run's copy on disk beside the writable layer, never in the tmpfs chroot.
  - guest-init drops the device's page cache (`BLKFLSBUF`) before reading it, because the capture boot's partition scan cached zeros.
- **Entropy and clock.** Every restore runs the resume hook (kernel reseed, clock step). The agent guest advertises the ADR-687 userspace-reseed capability like a warm builder: no process that consumes the run's randomness exists at capture. On metal, two restores read different `/dev/urandom` bytes and the host's time.
- **When schedd captures.** With `FAAS_SCHEDD_AGENT_RUN_SNAPSHOTS=1`, schedd captures the first time a shape cold-boots successfully on a node. The capture runs under that run's own ledger reservation, after its result has gone back to agentd, so admission accounting holds.
  - Capture keys are fixed per image and shape, so a later capture overwrites instead of adding storage.
  - A run that was offered a snapshot but cold-booted drops it, and the next cold run captures again.
  - The table lives in schedd's memory; a restart captures again.
- **Measured on gregale-internal-test-1** (`TestMetalAgentRun`, 1 vCPU / 1 GiB):
  - a snapshot holds 62 MB of content in its 1 GiB memory file;
  - restores reach the resume hook's ack in 0.44–0.49 s (1.4 s on a cold storage cache);
  - from wake to the result of a trivial command takes 0.62–0.67 s restored and 1.35 s cold-booted, about twice as fast.

  The harness's own start-up gain waits on the ADR-20261027090015 warm-up.
- **Found on the way.** As PID 1, guest-init reaped with `wait4(-1)` rather than `cmd.Wait`, so a fast command's output could be lost before the result frame. Agent runs now use owned pipes that are drained before the result.

**Compatibility check.** `gregale agent doctor` reports which parts of the contract a harness meets.

### 3. The run contract

**Inputs.**
- After guest-init unpacks the input drive (see Checkout below), the guest has the rendered prompt (`/input/prompt.txt`) and the triggering event (`/input/event.json`).
- It also holds a checkout, if requested (`/input/repo/`).
- In a workflow it holds earlier steps' files (`/input/steps/<name>/`).
- Gregale renders the prompt from the agent version's template and the event; the guest never renders templates.

**Environment.**
- Every base-URL variable named in `models` points at the relay endpoint (`GREGALE_OUTBOUND_URL`, ADR-989) for the agent's AI gateway integration.
- No model key, GitHub token or tenant secret is placed in the environment.

**Command.**
- The guest-init job supervisor runs `run` with `{prompt}` substituted, under the run's timeout.
- Exit 0 is success. Any other exit, a timeout or an OOM is a failure.

**Outputs.**
- `output/result.json` is inline. It is bounded by `AgentResultMaxBytes` and validated against the step's `output_schema` when one is declared.
- Other regular files under `output/` are streamed to object storage, bounded by count and bytes.
- Outputs travel over a bounded, framed vsock stream from the guest to vmmd, in the style of ADR-382. vmmd never mounts a filesystem the guest wrote.

**Checkout.**
- githubd fetches the commit archive at the requested SHA. It already holds installation tokens and streams source (`pkg/gitfetch`, `cmd/githubd/source_fetcher.go`). The archive goes into a per-repository cache, and later SHAs use `pkg/sourcedelta`.
- The input drive is a raw, read-only block device that carries an archive of the prompt, the event, the checkout and step files. guest-init unpacks it inside the VM, so no host component parses repository content.
- The checkout has no git history and carries no credentials.
- The per-repository cache is required, not an optimization. In the prototype (`scripts/experiments/agent-runs`), archives beat shallow clones by 3–4×. A 17.5 MB tree still took 5.6 s to fetch cold and 2.6 s to unpack on a laptop, while consecutive tags changed about 1 MB.

**As built in rollout step 8.**
- **The request.** A run names `checkout: {repository, commit}` with a full 40-character SHA. apid fixes the GitHub installation at admission: the one named, or the account's most recent; none is a 422 `agent_checkout_unavailable`.
- **Fetching.** agentd fetches the raw tar.gz through githubd's `StreamSourceRef` (the installation token stays in githubd), capped by `AgentCheckoutArchiveMaxBytes`, while renewing the run's lease. A cancel requested meanwhile ends the run before it boots.
- **The cache.**
  - The input drive is a 512-byte header (magic, size, SHA-256), then the archive, then zero padding to a sector.
  - It is stored content-addressed under `agents/inputs/images/<account>/<sha256>.img`. A ref object at `agents/inputs/refs/<account>/<installation>/<repo hash>/<commit>.json` maps the commit to that digest, so later runs of the commit skip GitHub.
  - Each agent node's storage cache keeps the images it used on local disk. That cache is the per-repository cache; `pkg/sourcedelta` transfers come later.
  - Caches are per account and per installation. Revoking a repository from an installation stops new fetches, but commits already cached stay usable until a retention sweep, which does not exist yet.
- **Attaching.** schedd and vmmd accept only keys under `agents/inputs/`, and only together with the request's checkout (digest, size, unpack limits). vmmd attaches the image as a read-only drive after the agent layer.
- **Unpacking.** guest-init checks the header and hashes the whole archive against the request. It then unpacks into `/input/repo`, owned by the harness's user, inside an `os.Root`:
  - the archive's top-level directory is dropped;
  - absolute and `..` names are skipped;
  - entries (`AgentCheckoutEntriesMax`) and bytes (`AgentCheckoutExtractedMaxBytes`) are bounded;
  - directories, files and symlinks are kept.

  A mismatch fails the run with `checkout`, an oversized tree with `checkout_too_large`.
- **Deviations.** Prompt, event and step files stay inline on the vsock request (≤ 2 MiB); only the checkout is on the input drive. The tree lives in drive1's free space until runs get a plan-sized scratch drive.
- **Gating.** agentd prepares checkouts only with `FAAS_AGENTD_CHECKOUTS=1`; otherwise such runs fail with `checkout_failed`.

### 4. Durable intent and state machine

New tables, all accessed through sqlc, with a CHECK on every state column:

| Table | Owner (writer) | Holds |
|---|---|---|
| `agents`, `agent_versions` | apid | Harness descriptor, prompt template, deliveries, limits and the automation link, all immutable per version |
| `agent_runs` | apid inserts `queued`; agentd owns every later transition (ADR-20261027090014) | Account, tenant, agent version, trigger, dedupe key, workflow run and step, chosen size, timings per stage, outcome, lease token, `gb_seconds` (ADR-20261027090011) |
| `agent_run_outputs` | agentd (from vmmd's stream) | `result.json`, file manifest and object keys |
| `agent_deliveries` | the delivery worker | Destination, idempotency key, attempts, outcome (`delivered`, `failed`, `outcome_unknown`) |
| `agent_size_history` | agentd | Per agent and input class: peak memory, pressure and outcome |

**States.**
- The run moves through `queued → starting → running → delivering`, then ends in one of `succeeded`, `failed`, `timed_out`, `out_of_memory` or `cancelled`. Terminal states are immutable.
- `starting` is the pre-dispatch state: an expired `starting` lease may requeue.
- `running` is the at-most-once fence: an expired `running` lease becomes `failed` and is never replayed as the same run. A rerun is a new attempt with a new generation (§5).

**Admission and claiming.**
- apid admits a run by serializing on the account (a per-account transaction advisory lock, so other writers of the account row never wait), then checking plan and tenant limits and the dedupe key, all in one transaction. A dedupe key already present returns the existing run. Dedupe keys are scoped to the agent and the tenant.
- agentd claims queued runs with `FOR UPDATE SKIP LOCKED` under an opaque lease token, the ADR-171 pattern. It then asks an agent node's schedd for an instance (ADR-20261027090014 §1).

**Cancellation.**
- Cancelling a `queued` run is terminal at once.
- Cancelling a `starting` or `running` run stops the VM after a short grace period.
- Deliveries never start for a cancelled run.

### 5. Admission, placement and sizing

**Admission.**
- A new ledger kind, `KindAgentRun`, is admitted only on the agent node pool (ADR-20261027090014 §2). Under the mixed role it uses only that node's agent share. An agent run never delays a tenant's wake, the same rule builds follow.
- Agent runs count toward invariant 2 through measured admission (ADR-909).

**Queues.**
- Queues are fair per tenant and bounded per account.
- A run that cannot be placed stays `queued` and reports its position. Admission beyond the account's queue bound is refused with an RFC 7807 limit error.

**Sizing.**
- A run without `ram_mb` gets a size from `agent_size_history`: p95 peak × 1.25, rounded up to 512 MB and capped at the plan's `AgentRunRAMMBMax`.
- The first run uses a default per preset and task class.
- An OOM triggers one rerun at the next size. The rerun is a new attempt of the same run, and is safe because nothing was delivered (§8).
- A need above the plan cap fails with a limit error that names the cap and the observed peak.

### 6. Execution path

**Booting.**
- schedd asks vmmd to restore the harness snapshot into a fresh jail, netns, cgroup and TAP, or to cold-boot if no snapshot is usable.
- It attaches the input drive and a scratch drive sized by plan.
- Invariant 5 holds: every run gets its own IP world (ADR-009), jail uid and an RNG reseed through the resume hook.

**The relay.** vmmd gives run VMs the ADR-989 relay listener; today only apps and executions have it.

**Running.** guest-init's job supervisor gains an agent mode: environment, command, output streaming and an exit report.

**As built in rollout step 5.**
- **Boot.** vmmd cold-boots runs on the execution-only path (`fcvm.Manager.WakeAgentRun`): a fresh jail, cgroup and lease with no tenant network. drive1 is a platform agent layer whose only workload file is `/etc/faas/agent.json`, which selects guest-init's agent mode.
- **Protocol.** The command, environment and input files cross afterwards on `pkg/agentrunproto` (vsock 1033): one request, then bounded stdout and stderr frames, then one result with `result.json` and the output files inline (ADR-382 style).
- **Exit report.** The guest reports `out_of_memory` from its kernel's `oom_kill` counter, because an OOM kill otherwise looks like any SIGKILL.
- **Boot-path fixes.** The first metal run of a networkless cold boot found two bugs, both shared with executions:
  - Firecracker 1.7 rejects `"network-interfaces": null`;
  - networkless jails never received the `/dev` tmpfs and `/dev/kvm` node, which networked boots get from the TUN step.
  Networkless boots now get both devices from the same mount helper, and still no TUN device.
- **Later steps.** The relay comes in step 7 (no TAP is needed for it), the input drive in step 8, and network egress with web access (§12). Until then the run's scratch is drive1's free space.

**Teardown.**
- On exit, timeout, OOM or cancel, vmmd destroys the VM and its drives, then reports the outcome, peak memory and the ADR-20261027090011 memory integral.
- agentd records the terminal state only after schedd confirms teardown.

**As built in rollout step 6.**
- **The chain.** agentd claims a run and opens schedd's `RunAgent` stream. schedd admits a `KindAgentRun` reservation and calls vmmd's `RunAgent`, which boots the VM and speaks `pkg/agentrunproto` to the guest. Each hop is one bidirectional stream: start and cancel go down; placement, boot, output and the result come back up.
- **The fence handshake.** After admitting, schedd sends `placed` and waits. agentd records `running` (node and instance), then sends `proceed`. Only then does schedd ask vmmd to boot. So a VM never boots for a run still in `starting`, and an expired `starting` lease can requeue it safely. If the stream ends before `proceed`, schedd releases the reservation with `Aborted` and nothing boots.
- **Outcomes agentd records.**
  - Before placement:
    - no capacity, or schedd unreachable → the run is requeued;
    - outside the plan (`FailedPrecondition`) → `failed` / `rejected`.
  - After placement, a lost stream → `failed` / `transport_failed`. It is never replayed.
  - An expired `running` lease is failed with `lease_lost` by a sweep that every agentd replica runs.
  - A cancel requested in the database reaches the stream on the next lease renewal (every third of the TTL).
- **Account concurrency.** agentd enforces `AgentRunConcurrentPerAccount` at claim time. It takes apid's per-account advisory lock with a non-blocking try-lock, so a claim skips a busy account instead of waiting on apid.
- **The agent pool.** Until `compute_nodes` carries the agents role (ADR-20261027090014 §2), the pool is schedd's `FAAS_SCHEDD_AGENT_RUN_NODES` allowlist.
- **No instances rows.** Run VMs (instance `agent-<run id>`) have no instances rows; the ledger reservation lives exactly as long as the RPC.
- **Outputs.** `agent_run_outputs` holds `result.json`, the file manifest (name, size, sha256) and the last 64 KiB of the log. With `FAAS_AGENTD_OUTPUT_FILES=1`, file contents are kept too (rollout step 10, below).
- **Teardown gap.** vmmd sends the result after its destroy attempt, but it does not yet report a failed destroy, peak memory or the ADR-20261027090011 memory integral. Those come with billing (step 15) and sizing (step 13).
- **Gating.** `FAAS_AGENTD_AGENT_RUNS`, `FAAS_SCHEDD_AGENT_RUNS` and `FAAS_VMMD_AGENT_RUNS` are all default off. agentd has no systemd unit or Ansible role yet.

### 7. Model access and spend

- **Identity.** outboundd maps the relay assertion to the run, its tenant and its account. It sets `X-Gregale-AI-User` to the tenant from that mapping and ignores any value the guest sends.
- **Budgets.** Per-run and per-workflow spend budgets are reservations in the AI gateway (ADR-990, 991, 1120). A run or workflow that would exceed its budget is refused at the gateway, and the harness sees an error response.
- **Keys.** The provider key lives in an AI gateway integration (D8: bring your own key), and the run reaches it through the relay. Claude Code officially supports this LLM-gateway pattern: the provider key stays on the server side, and usage is billed to whoever owns the forwarded credential.
- **Claude Code credentials.**
  - For a run of the Claude Code binary, the forwarded credential must belong to the end user the run serves.
  - For a seller's tenant, that is the tenant's own Anthropic API key or third-party provider credential, stored per tenant in the AI gateway.
  - A seller's own key serves only the seller's own authorized users, for example its staff, or Gregale dogfooding on its own repository.
  - Gregale never supplies Claude credentials, never bills Claude usage, and never collects or stores Claude-account (subscription) sign-in tokens. Automated runs therefore use API keys or third-party provider credentials only.
  - The gateway forwards the headers that Claude Code's gateway compatibility guide requires.
- **Other harnesses.** Codex and OpenCode are not covered by the Claude Code conditions. A seller using them, or Anthropic's Agent SDK, with its own key to power its own product follows the provider's ordinary API terms. For Anthropic, those terms allow powering products for one's own customers (Commercial Terms §A.1) and prohibit reselling the Services without approval (§D.4).
- **Model resale.** Any future Gregale-supplied model offering through the AI gateway is reselling and needs the provider's approval. It never applies to Claude Code runs.

**As built in rollout step 7.**
- **Integrations on the version.** A version names one AI gateway integration per model provider (`integrations`). apid accepts only the account's enabled customer AI integrations for that provider, checked in the version's transaction. agentd sets each provider's base-URL variable to `$GREGALE_OUTBOUND_URL/i/<integration>` (`/v1` for OpenAI) and a placeholder key the relay replaces; a provider without an integration gets nothing.
- **The relay.** guest-init's agent mode brings loopback up and serves the ADR-989 relay. agentd sends the run's relay identity with the start frame: lease token, tenant, agent and version. schedd and vmmd carry it to the VM. vmmd then mints an `agent_run:<id>` assertion from that placement data (ADR-20261027090016 claims), never from the guest.
- **outboundd.** Behind `agent_runs_enabled` (default off), every request checks:
  - the run is `running` under that lease, with no cancel requested;
  - the tenant and version match;
  - the version names the integration for its provider.

  The tenant replaces any guest-sent `X-Gregale-AI-User`, and the run is the session. Usage rolls up under the `runs` caller until tenant attribution (step 14).
- **Per-run spend caps.**
  - The cap is the version's `ai_budget_millicents`, or the plan default ($0.50 / $2 / $5). agentd records it on the run with the `running` fence.
  - outboundd holds each call's ADR-991 cost estimate in `agent_run_ai_reservations`, keyed by the admission lease, and settles actual cost into `agent_run_ai_spend`.
  - A call is refused with 403 `outbound_agent_run_budget_exhausted` when settled spend plus live reservations plus the call would pass the cap. It is a 403, not a 429, because the cap never resets.
  - A billable call the gateway cannot price is refused (`outbound_agent_run_unpriced`), because the cap could not bound it.
  - A crashed gateway's reservation lapses after the request timeout plus 60 s.
- **Not yet.** The per-account daily cap ($10 / $50 / $200), Bedrock and Vertex for Claude Code, and per-tenant provider credentials. A metal run of a real harness through the relay is still owed.

### 8. Deliveries

- A delivery worker beside outboundd performs deliveries. outboundd already holds platform credentials (ADR-249).
  - GitHub comments use githubd's installation token.
  - Email uses the mail pipeline.
  - Webhooks use the webhook delivery ledger.
  - Files go to buckets.
- A delivery starts only after the run reaches `delivering`. A run that failed, timed out, hit an OOM or was cancelled delivers nothing, which makes reruns safe.
- Each delivery carries an idempotency key derived from the run (for example, automation + PR + head SHA). A re-triggered run therefore updates the previous PR comment instead of adding another.
- A delivery whose response is lost becomes `outcome_unknown` and is never blindly retried. It is reconciled against the destination where the destination supports that, and surfaced otherwise.
- An agent can approve, merge, push or post elsewhere only through a delivery action its version declares.

**As built in rollout step 10, first part.**
- **Run webhooks.** An account receiver (ADR-224) can subscribe to `agent_run.finished`. Only accounts with the agents capability may add the event; for any other account it is refused like an unknown event.
  - Deliveries use the shared signed ledger (`app_webhook_deliveries`, account scope, no app). Signing, retries, attempt history and dead letters are therefore the existing ones.
  - The delivery row is written in the transaction that ends the run, on every path: agentd's finish, a cancel while queued, and a running run whose lease expired. A run cannot end unannounced. The run ID is the row's `source_event_id`.
  - Only a succeeded run carries its result and file manifest. A result up to 64 KiB (`AgentRunWebhookResultMaxBytes`) is inline; a larger one is marked `result_omitted`. Every other end carries its state, outcome and exit code only.
  - A webhook is queued atomically with the terminal state, and its fate cannot change the run's outcome. For a run with declared deliveries, that is the state the run reaches after `delivering` (second part).
- **Outputs.** `GET /v1/agent-runs/{id}/outputs` returns the stored result, file manifest and log tail, and answers 409 `agent_run_not_finished` before the run ends. It is hidden with the rest of the agent API and left out of the spec and SDKs.

**As built in rollout step 10, second part: GitHub comments.**
- **Declaring and targeting.**
  - A version declares `deliveries: [{type: "github_comment", field: "comment"}]`, each type at most once (`AgentDeliveriesPerVersionMax`). The field names the `result.json` string that becomes the comment; it defaults to `comment`.
  - A run names its target as `github: {repository, number}`. apid fixes the account's installation at admission, as for checkouts.
  - A version that declares a comment refuses a run without a target (422 `agent_delivery_target_missing`); a target without an installation is 422 `agent_github_unavailable`.
- **`delivering`.** When the harness succeeds and the version declares deliveries, the finishing transaction does three things together: it moves the run to `delivering` and drops its lease (the VM is already gone), records the outputs, and writes one `agent_deliveries` row per declared delivery.
  - A result without the field, or a replica started without `FAAS_AGENTD_DELIVERIES=1`, fails the delivery at once instead of leaving the run waiting.
  - A comment over 60,000 bytes (`AgentGitHubCommentMaxBytes`) is cut at a character boundary and marked `truncated`.
  - The run still counts against the account's concurrency while it delivers.
- **The worker.** It runs in agentd, which owns `agent_deliveries` (ADR-20261027090014). It claims due rows under a lease token with `FOR UPDATE SKIP LOCKED` and calls githubd's new `UpsertIssueComment` on the local socket. githubd checks the installation belongs to the account; the token never leaves githubd.
- **One comment per agent and issue.** The idempotency key is a digest of account, agent, tenant, repository and number, carried as a hidden marker (`<!-- gregale:agent-… -->`). A later run therefore updates the agent's comment instead of adding another.
  - Only comments written by a bot count as carrying the marker, so a person quoting it does not capture the comment.
  - A waiting delivery gives way to a newer one for the same key: it ends `superseded`, and its run counts it as done.
  - A partial unique index allows one `sending` row per key, so two writes to one comment never overlap.
- **Lost answers.** githubd reads the issue before every write, paging through up to 2,000 comments, and patches or posts. When a write's answer is lost, it reads the issue again and confirms the comment.
  - A refusal (403, 404, 422) fails the delivery.
  - A rate limit or a write that did not land waits with backoff (10 s doubling to 2 min, at most 8 attempts).
  - Anything that may have written moves to `reconciling`: the next attempt reads GitHub first, so the retry is reconciled, not blind.
  - A delivery still unresolved when its 15-minute window (`AgentDeliveryWindowSec`) closes ends `outcome_unknown`, and a send whose worker vanished is reconciled the same way.
- **How the run ends.** The delivery that ends last settles its run, locking the run row so two deliveries ending together cannot both miss it; a sweep catches any run left over. The run ends:
  - `succeeded` when every delivery was delivered or superseded;
  - `failed` with `delivery_failed` or `delivery_outcome_unknown` otherwise;
  - `cancelled` when a cancel stopped a delivery before it started.

  Only then is `agent_run.finished` queued, with each delivery's state, attempts and comment URL. The outputs endpoint reports the same list.

**As built in rollout step 10, third part: output files, buckets and metrics.**
- **Output file contents.** With `FAAS_AGENTD_OUTPUT_FILES=1`, agentd writes each file of a succeeded result to the artifact store at `agents/outputs/<account>/<run>/<sha256>` before it finishes the run. It checks each file's size and digest first and records the key in the manifest.
  - A file that cannot be kept, after three tries, fails the run with `outputs_unavailable`; a run never finishes with files no one can read.
  - `GET /v1/agent-runs/{id}/outputs/files/{name}` streams one kept file as an attachment (`application/octet-stream`, `nosniff`), hidden like the rest of the agent API. Keys never appear in API responses.
  - There is no retention sweep yet; kept files stay until one exists.
- **Bucket deliveries.**
  - **Declaring.** A version declares `{type: "bucket", bucket: <bucket ID>, prefix}`; the prefix defaults to `agent-runs/` and must be a clean key prefix ending in a slash. The store checks that the bucket is the account's when the version is written, and records the app that owns it (422 `agent_bucket_not_found` otherwise).
  - **Writing.** The delivery writes every output file to `<prefix><run id>/<name>`.
    - Each file goes through the S3 gateway on a one-shot upload grant (`state.ObjectUploadGrant`, 5 minutes) that agentd issues for exactly that key, size and headers. No credential leaves the platform.
    - The gateway applies the bucket's quotas and write fences like any other write. Objects carry `x-amz-meta-gregale-agent-run` and `x-amz-meta-sha256`.
    - The delivery's URL is `s3://<bucket>/<prefix><run id>/`.
  - **Ending early.** A run without files has nothing to write and ends delivered (`no_output_files`). Files whose contents were not kept fail the delivery at once (`output_files_unavailable`).
  - **Failures.**
    - A missing bucket or a gateway refusal fails the delivery.
    - 429 and 503, a fenced bucket, or a bucket not yet ready wait with backoff.
    - A lost answer or another 5xx moves to `reconciling`. Rewriting the same bytes to the same key is harmless, so the next attempt writes again; in a versioned bucket that can leave an extra version.
  - **Switches.** Bucket deliveries need `FAAS_AGENTD_DELIVERIES=1` and the gateway's https origin in `FAAS_AGENTD_S3_ENDPOINT`. Without the origin they fail as `buckets_unavailable`.
  - **Keys.** A bucket delivery's idempotency key is the run's own, so runs never supersede each other's files.
- **Metrics.** agentd serves `/metrics` on `FAAS_AGENTD_METRICS_ADDR`:
  - `agent_deliveries_total{destination,outcome}`, every delivery that ended (delivered, failed, outcome_unknown, superseded, cancelled);
  - `agent_delivery_attempts_total{destination,result}`, with result delivered, retry, rejected or unknown;
  - `agent_delivery_attempt_seconds{destination}`.

  Deliveries that fail before any attempt (no comment field, kept files missing, deliveries off) are not counted yet.
- **Not yet.** Declared delivery actions beyond commenting, files and email.

**As built in rollout step 10, fourth part: email to the account's own address.**
- **Declaring.** A version declares `{type: "email", field}`; the field defaults to `summary`. There is no recipient to name: the summary goes only to the account's own address. Tenants are reached through the seller, never mailed by Gregale.
- **Capture.** The finishing transaction writes an `email` delivery row with the summary string, as for comments. apid's producer mail outbox captures it the way it captures deployment failures, into the sealed `agent_run_delivered` template. `agent_deliveries` keeps one writer: the outbox only reads the row, sharing its lock while it captures, and writes its own receipt (`platform_mail_agent_run_observations`).
  - **Payload.** The payload is sealed with the outbox's credential identities. It holds only the agent's name, the run ID, the finish time and the first 1,200 bytes of the summary (`InternalMailAgentRunSummaryMaxBytes`), redacted (ADR-096), with each line's control characters neutralized.
  - **Bounds.** The bound keeps the ciphertext under the outbox's 4 KiB limit. The mail says when the summary was shortened, and points to the outputs endpoint.
  - **Dispatch.** Dispatch rechecks the account under its lock: an address change, a suspension or a pending deletion cancels the intent. Suppression, retries, expiry (24 h), recovery holds and reconciliation are the outbox's own.
  - **First capture.** It records where it started, so enabling the template never mails runs that finished before it.
- **How the delivery ends.** agentd never claims an email delivery; its claim pass ends it from the receipt.
  - A `queued` receipt ends it delivered.
  - `unavailable`, `stale` or `expired` ends it failed (`mail_<disposition>`).
  - With no receipt, the delivery window closes and it fails as `delivery_timed_out`.

  The outbox refuses deliveries less than a minute from their deadline (`expired`), so a summary is never mailed for a delivery agentd reports as failed.
- **Switches.** `FAAS_INTERNAL_MAIL_AGENT_RUN_DELIVERED_OUTBOX_ENABLED=1` on apid and maild, on top of the producer outbox gates, and `FAAS_AGENTD_DELIVERIES=1` on agentd. The template has maild's independent producer, generation and admission gates like every other.
- **Schema.**
  - The outbox's template CHECKs are widened NOT VALID, then validated, and `agent_delivery_id` ties an intent to its delivery.
  - The indexes are built concurrently in a separate migration.
  - The down migration refuses to discard any intent, receipt or reconciliation history.

### 9. Triggers, automations and the seller API

- **Automations (ADR-488)** gain an `agent` step. The one-sentence create flow produces an automation draft and tests it through simulation (ADR-575) before publication.
- **githubd** starts automations from `pull_request` events (today it uses them for preview environments). Signed webhooks start automations generally, beyond the Stripe-only starts of ADR-574.
- **Triggers (ADR-100)** gain agents as a target.
- **The seller API.**
  - `POST /v1/agents/{agent}/runs` takes `{tenant, prompt?, inputs, dedupe_key?}` and returns `202` with the run ID.
  - `GET /v1/agent-runs/{id}` reads a run, with an SSE stream for progress; `DELETE /v1/agent-runs/{id}` cancels it.
  - A completion webhook fires when a run ends.
  - Tenant identity may come from the seller's own JWT (ADR-244).

**As built in rollout step 11, first part: the automation agent step.**
- **Declaring.** A workflow step names `agent: <name>` (instead of `run:`). Its `input` is rendered by the existing template language, so a whole-value reference keeps its JSON type. The result is the run request: `version`, `prompt`, `inputs`, `checkout` and `github`. Without an input, the workflow run's input becomes the run's `inputs`.
  - An agent step takes only `input`, `depends_on`, `when` and `timeout` (1 minute to 24 hours, default 90 minutes).
  - Retries and failure routes are not offered yet: a rerun is a whole new agent run, and step 13 brings OOM reruns.
- **Running.** schedd's orchestrator admits one run per step attempt through the same admission as the agent API: plan limits, tenant, installation and the deliveries' targets.
  - The run is attributed to the workflow run's platform tenant.
  - It is idempotent through a dedupe key per workflow run, step and attempt, so a scheduler that stops between admission and parking admits nothing twice.
  - The run records its workflow run, step and a reserved event name (`workflow.agent.<uuid>`). The step parks on that event the way callback steps park (ADR-263), holding no connection.
- **Finishing.** A schedd watcher (`DeliverAgentRunEnds`, every workflow tick) lists finished linked runs whose still-active workflow run has no such event yet, and delivers each end as the event. The event is the receipt. agentd writes only the run; the orchestrator writes only workflow state.
  - The step succeeds with the run and fails with it (`agent run <id> ended <state>: <outcome>`).
  - Its output is `{run_id, state, outcome, result}`, with `result.json` inline up to 64 KiB (`WorkflowAgentStepResultMaxBytes`), so later steps read `steps.<name>.output.result.…`.
  - Customers can neither wait for nor inject the reserved event names.
- **Gating.** apid refuses agent steps without the agents capability, like any invalid step target. This applies in the automation API (save and validate) and every deploy and manifest path, including GitHub push builds. schedd runs them only with `FAAS_SCHEDD_AGENT_STEPS=1`. The `agent` field stays out of the public spec through a preview-field exclusion.
- **Not yet.**
  - Cancelling the agent run when its workflow run is cancelled.
  - `input_files`, `output_schema` and the workflow budget (step 12).

**As built in rollout step 11, second part: pull requests start automations.**
- **The event.** A pull request becomes a platform event of the installation's account: source `gregale.github`, type `pull_request.<action>`, for the actions `opened`, `reopened`, `synchronize`, `ready_for_review` and `closed`.
  - `gregale.*` sources are already reserved, so customers cannot publish one.
  - The event is bounded (`api.GitHubPullRequestEvent`): the installation, the repository, and the pull request's number, title, state, draft, merged, `fork`, author, URL, labels, and head and base refs and SHAs.
  - It carries no body or diff; the agent reads those through its checkout and GitHub target.
  - The title is cut at 1 KiB and the labels at 20 of 100 bytes each (`GitHubEventTitleMaxBytes`, `GitHubEventLabelsMax`, `GitHubEventLabelMaxBytes`).
  - `fork` is true when the head is in another repository, or when GitHub no longer names the head repository.
- **Starting.** Nothing new is declared. The existing event trigger names the source, and its filter can keep forks out:
  `trigger: {type: event, source: gregale.github, event_type: "pull_request.*", filter: {data: {pull_request: {fork: false}}}}`.
  - The workflow input is the event envelope, so an agent step's input names `{{input.data.repository.full_name}}`, `{{input.data.pull_request.number}}` and `{{input.data.pull_request.head.sha}}` for its `github` target and `checkout`.
  - The account's event subscriptions match the source too.
- **The path.** githubd's delivery worker builds the event from each `pull_request` delivery before the preview path runs, decoding the delivery on its own. It hands the event to apid over the existing bridge socket (`PublishGitHubEvent`).
  - apid finds the installation's account. It publishes only for an active account with the agents capability, through the ordinary published-event acceptance, so the events trigger captures the matching workflows in the same transaction.
  - Platform events are never charged to the account's event storage, so its limits do not refuse them.
  - The delivery GUID is the event ID, so the worker's retries publish nothing twice.
  - A refusal that a retry cannot fix completes the delivery without the event: malformed, conflicting, or an apid without the RPC. Only an unavailable apid fails the delivery for a retry.
- **Switches.** `FAAS_GITHUBD_AUTOMATION_EVENTS=1` on githubd (it needs `FAAS_APID_GITHUBD_BRIDGE_SOCK`), on top of the agents capability on apid.
- **Tested end to end.** `pkg/sched` admits the agent run from a published `gregale.github` event. The step renders its `github` target and `checkout` from the event, in memory and on Postgres.
- **Found while building.** The preview path's decoder (`DecodePullRequest`) reads `pull_request.head_sha`, which GitHub's payload does not have; the SHA is under `pull_request.head`. It also fails on actions it does not handle. So real pull request deliveries fail preview decoding and are retried until they are dead-lettered. Automation events are forwarded before that decoder runs, so they are unaffected. Fixing the decoder would start previews for real and is left to its own change.

**As built in rollout step 11, third part: signed webhooks.** Nothing was built. Generic signed webhooks (ADR-1387) already start automations beyond Stripe:
- An inbound endpoint with provider `generic`, bound to a workflow by the automation binding (ADR-574), publishes each verified delivery as a `gregale.inbound.generic.<endpoint>` event.
- The workflow run's input is the envelope, so an agent step reads `{{input.data.…}}` and `{{input.type}}`.
- The webhook path checks a definition with the same DAG validation as every other start, and agent steps need no outbound binding, so an agent step runs under it unchanged.
- `pkg/sched` tests the chain from an accepted generic delivery to the admitted agent run.

**As built in rollout step 11, fourth part: agents as a trigger target (ADR-100).**
- **Declaring.** A broker trigger (`kafka`, `nats`, `redis_streams`, `sqs_compat`) names `agent: <name>` at create. Each record then starts a run of that agent's latest version instead of invoking the app.
  - The agent is fixed at create, like the kind. It requires record delivery.
  - apid checks that the account has the agent.
  - `triggers.agent` carries it, with a CHECK on the kinds, the delivery and binding-owned projections.
  - apid creates the trigger disabled, sets the agent (`SetTriggerAgent`, as ADR-1321 sets batch delivery), then enables it. A trigger whose agent cannot be set is deleted again, so no record reaches the app instead.
- **Running.** schedd's trigger tick polls, filters, persists and claims as for any trigger. It then admits one run per record through the agent admission and its plan limits, with trigger `trigger`, in place of the gateway post.
  - The run's inputs are `{trigger: {id, kind, slug}, record: {item_identifier, payload, headers, metadata}}`. The payload is JSON, since trigger records store payloads as jsonb.
  - Inputs over `AgentRunInputsMaxBytes` (256 KiB) dead-letter the record as `payload_too_large`.
  - The dedupe key hashes the trigger and the broker's message identity, or the record's row when the broker has none, so a redelivered record admits nothing twice.
- **Outcomes.** The record succeeds once its run is admitted; the run's own deliveries and completion webhook report its end.
  - A plan limit retries the record under the trigger's retry policy.
  - A refusal no retry can fix dead-letters it as `customer_disabled`: a deleted agent, or an agent that declares a GitHub comment, which a trigger's run has no target for.
  - The app's wake rate limits do not apply, since no app wakes.
- **Gating.** The `agent` field exists only with the agents capability: without it, apid refuses it exactly as an unknown field. It stays out of the public spec through preview-field exclusions.
  - schedd polls agent triggers only with `FAAS_SCHEDD_AGENT_TRIGGERS=1`. Without it their records stay with the broker.
- **Environment clones.** The clone schema registry (ADR-590) now lists every agent table, which earlier steps had missed:
  - agents and their versions as account identity, never cloned with an environment;
  - runs, outputs, spend, reservations and deliveries as operational;
  - the agent-run mail capture tables, `platform_mail_producer_outbox.agent_delivery_id` and `triggers.agent`.
- **Not yet.**
  - Declaring agent triggers in `gregale.yaml`.
  - Queue, realtime and cron triggers as agent targets.
  - A prompt template over the record.

**As built in rollout step 11, fifth part: the PR-triage chain on metal, and staging the agent drives.**
- **The chain test.** `TestMetalAgentPRTriage` (`pkg/e2etest/agentchain`) runs the whole PR-triage path on a native KVM host with the real daemons: a signed `pull_request` delivery to githubd, the `gregale.github` event, the automation's agent step, agentd's claim, schedd's admission, vmmd's boot, the relay to outboundd, and the result back on the workflow run.
  - `scripts/experiments/agent-runs/metal/run_agent_chain_metal.sh` runs it under the host acceptance lock with a private Postgres, and runs leakcheck before and after.
  - Without a provider key, a stand-in harness sends one request through the relay with a placeholder credential. Anthropic's 401 proves the call reached the provider.
- **What running it found.**
  - githubd ignored `--config`; it now reads the flag, like outboundd.
  - vmmd refused every agent run's boot: the scan gate (issue #299) found no scan sidecar for the agent base, because nothing staged one.
  - Pro runs (2 vCPU) could not boot: the cgroup writers accepted only the app CPU tiers (250/500/1000 millicores). An agent run's networkless lease now gets whole vCPUs up to the largest `AgentRunVCPUMax`. The value comes from one function (`leaseCPUMax`) for both the written `cpu.max` and the fence record. App leases keep the tiers.
  - A failed boot showed only as `transport_failed`. agentd now logs why the run's stream ended.
  - Every model call from a run was refused with a 503 `outbound_admission_unavailable`. The Postgres admission backend refuses a customer integration admitted without a binding app, so an app call can never skip its binding's budget. An agent run has no binding: its run grant authorizes the call.
    - `AdmissionSpec.AgentRun` now lets such a call through; the integration's own limits still apply.
    - The in-memory backend never had the check, so the unit tests had passed.
    - outboundd now logs admission errors, which callers see only as a generic 503.
  - Once those were fixed, the chain passed keyless on gregale-internal-test-1 (2026-10-22):
    - the PR delivery to the succeeded workflow run took 55 s;
    - the step resumed within a second of the run's end;
    - the run's relay call reached Anthropic, which refused the placeholder key with a 401, and outboundd recorded one model call against the run.
- **Staging the agent drives.** An agent-pool imaged stages both drives when `FAAS_AGENT_BASE_REF` pins a digest of `images/agent-base`.
  - drive0 takes the standard digest-verified base path: guest-init is installed, and the Grype scan sidecar the boot gate reads is written. It lives at `base/runner-agent-amd64.ext4`.
  - drive1 is the platform agent layer at `base/agent-layer-v1-amd64.ext4`: a 512 MiB ext4 holding only a root-owned `upper/etc/faas/agent.json`, published and signed like a base. The key's version names its content, so imaged publishes it once and a new layout takes a new key.
  - Unset, imaged stages neither, so other nodes carry no agent residency.
  - schedd's `FAAS_SCHEDD_AGENT_RUN_BASE_KEY` and `FAAS_SCHEDD_AGENT_RUN_LAYER_KEY` default to these keys. Setting them overrides the defaults, as the metal tests do with local files.
- **The imaged-staged base on metal.** With `FAAS_E2E_AGENT_BASE_LAYER`, the chain test has the harness's imaged stage the agent base from a local registry, as an agent-pool node does. The root filesystem is built from `images/agent-base.Dockerfile` step for step (`build_agent_base_layer.sh`), since the host has no BuildKit. The chain passed this way on gregale-internal-test-1 (2026-10-23):
  - Grype on the agent-base contents: 0 critical, 0 high, 5 medium, 1 unknown. The only finding with a fix available is the unknown one, so vmmd's gate admitted the base.
  - The base is a 2.3 GB ext4. Its scan took 10 minutes on the 2 vCPU host, and imaged was ready after 17.
  - The first cold boot of a run took 54 s, of which the cold boot itself (`cold_boot_ms`) was 5 s.
    - The other 48 s was vmmd hashing the 2.3 GB base, in full, on the wake path. It records the VM's snapshot backing identity (ADR-510) after every boot, and memoizes the digest per file version. So the first boot of each base version per vmmd process paid it.
    - Startup priming covers only the configured runtime bases, never the agent base. The earlier run with a 1.3 GB test base paid 29 s the same way.
    - vmmd now pins the booted files and hashes them in the background; captures wait for the identity. Boots that skip readiness, agent runs among them, now log their cold-boot breakdown, which they never did.
    - Confirmed on gregale-internal-test-1 (2026-10-27): the first run's wake took 323 ms in total, with a 317 ms cold boot, against 53.8 s before. The PR delivery reached a succeeded workflow in 10 s.
    - The earlier 5 s cold boot did not recur, so it was host load rather than the boot path. One per-run cost remains on hosts without reflink, such as this one's ext4: the 512 MiB agent layer is copied into the tmpfs jail. Production nodes clone it on XFS.
  - Running it found two imaged bugs:
    - A shared base's scan ran under the five-minute artifact timeout, so the agent base always stayed fail-closed and every run would have been refused. Shared bases now take `ApplicationStandardBaseScanTimeout` (20 min).
    - A failed scan stored only its failure code. imaged now logs the reason and its cause.
- **Not yet.**
  - The pmem attachment of the base. The harness runs Firecracker 1.7, so vmmd attaches the pmem-labelled base as a block drive.
  - Signature checks on the agent layer at admission. App layers are checked there; agent runs are not yet.

### 10. The workflow agent step

- **Executor.** `WorkflowOrchestrator` (`pkg/sched/dispatch_workflows.go`) gains an executor for steps with `agent:` in place of `run:`. It inserts the run with the workflow run and step IDs, then waits for the run's terminal event the way callback steps wait (ADR-263). It does not hold a connection open.
- **Outputs.**
  - `steps.<name>.output` is the run's `result.json`.
  - `steps.<name>.files.<key>` refers to its stored files.
  - `input_files: [step, …]` mounts earlier steps' files read-only under `/input/steps/`.
- **Schema validation.** When `output_schema` is declared and validation fails, the step reruns once with the validation error appended to the prompt. If it fails again, the step fails.
- **Budget.** A workflow `budget.spend` is one reservation scope shared by every agent step and `for_each` branch. Concurrent agent branches are capped by `AgentForEachConcurrentMax`.
- **Retries and recovery.** These follow the existing workflow semantics: a failed step retries alone, and completed steps' outputs are durable.

### 11. Tenancy

- **Attribution.** Every run, output, delivery and metering record carries the tenant.
- **Limits.** Per-tenant limits default to the account's and can only be lowered.
- **Isolation.** Cross-tenant reads of runs, outputs, transcripts, connections and streams fail at every layer.
- **Statements.** Tenant statements (ADR-238, 248) gain agent GB-seconds and model tokens as usage sources. Pricing for tenants stays the seller's.
- **Connections.** Per-tenant OAuth connections (each customer's own GitHub, CRM or Slack) are a later change. Until then, runs act only through the seller's account-level connections.

### 12. Security

- **Untrusted inputs.** PR content from forks and fetched web pages are untrusted, including prompt injection. Defenses:
  - a read-only checkout and no credentials in the VM;
  - a default "do not run code" prompt for triage-class presets;
  - brokered, length-capped deliveries;
  - declared delivery actions only (§8).
- **Egress.** ADR-361 default-deny applies unchanged: TCP 80 and 443, per-VM connection rate limits, and private, link-local and metadata ranges unreachable.
- **Web access** follows ADR-20261027090014 §4:
  - tier 0: model-provider web tools;
  - tier 1: managed `web.fetch` and `web.search` through the relay;
  - tier 2: an opt-in browser preset.

  All of it goes through the dedicated agent egress gateway and politeness proxy, under an enforced acceptable-use policy. No web-capable agent runs for a customer until those controls exist and the owner has tested them on production.
- **Unchanged.** cgroups v2; the jailer (unique uid/gid, chroot, seccomp); `memory.max` = size + 8 MB; block devices only; virtio-rng.

## Limits (`pkg/api/limits.go`)

Free is 0 for every limit. Values are per plan as Hobby / Pro / Scale.

| Name | Value | Basis |
|---|---|---|
| `AgentsPerAccount` | 10 / 50 / 200 | Agent definitions |
| `AgentVersionsPerAgent` | 50 / 200 / 1,000 | Immutable versions kept per agent |
| `AgentRunConcurrentPerAccount` | 2 / 5 / 20 | Matches the plans' concurrent-instance ceilings |
| `AgentRunQueuedPerAccount` | 20 / 100 / 1,000 | The per-account queue bound of §5 |
| `AgentRunTimeoutMaxSec` | 300 / 1,800 / 3,600 | Matches `JobTaskTimeoutSec` |
| `AgentRunRAMMBMax` | 1,024 / 2,048 / 4,096 | Hobby is above `JobRAMMB`, because Node-based harnesses do not fit 512 MB |
| `AgentImageLayerMaxMB` | 512 / 1,024 / 2,048 | Matches `AppLayerMaxMB`; the shared drive0 is not counted |
| `AgentRunsPerDay` | 500 / 10,000 / 100,000 | Runaway guard |
| `AgentWorkflowStepsMax` | 5 / 20 / 50 | |
| `AgentForEachConcurrentMax` | 2 / 5 / 20 | Never above concurrent runs |
| `AgentResultMaxBytes` | 64 KiB / 256 KiB / 1 MiB | Inline `result.json` |
| `AgentOutputRetentionDays` | 7 / 30 / 90 | Object storage held |
| `AgentOutputBytesPerAccount` | 1 GB / 10 GB / 50 GB | Object storage held |
| Default model spend cap, per run / per day | $0.50 / $10; $2 / $50; $5 / $200 | Seller-adjustable defaults, enforced in the AI gateway |

`docs/plans.md` is regenerated with `make pricing-md` when these land.

## Metrics (§12 additions)

- `agent_runs_total{outcome}`
- `agent_run_start_seconds{stage}`, with stages `queue`, `boot`, `input`, `harness` and `delivery`
- `agent_run_duration_seconds`
- `agent_run_queue_depth`
- `agent_run_oom_reruns_total`
- `agent_deliveries_total{destination,outcome}`

The per-run timeline in the API and CLI shows the same stages. Billing metrics are in ADR-20261027090011.

## Rollout

Each step is one PR behind the switches. PRs that touch the VM lifecycle run `make test-metal` and `make leakcheck`.

1. This ADR, ADR-20261027090011 and ADR-20261027090012; the financial-model update.
2. Migrations, the state store and the limits.
3. apid `pkg/apid/agents`: definitions and admission (intents only), plus the gating.
4. The `agent-base` image (`images/agent-base.Dockerfile`): the shared drive0 of §2 with all three preset harnesses, Claude Code as the unmodified published binary. The presets are the catalog in `pkg/agentpresets`, not separate images, because §2 counts their binaries once in base residency. Harness versions are pinned by an integrity-checked npm lockfile.
5. The guest-init agent mode; vmmd run boot (cold) and output streaming.
6. agentd claim and lease, schedd admission on the agent pool (`KindAgentRun`), teardown and terminal states (ADR-20261027090014).
7. The relay for run VMs, base-URL environment, tenant identity, and per-run budgets.
8. Checkout and the input drive.
9. Preset snapshots.
10. Deliveries.
11. GitHub and webhook triggers, the automation `agent` step, and the ADR-100 target. **Milestone A:** PR triage dogfooded on Gregale's own repository.
12. The workflow `agent:` step, `output_schema`, and the workflow budget.
13. Sizing history, OOM rerun, and fair queues.
14. Tenant attribution, meters, seller API streaming and webhooks.
15. Billing (ADR-20261027090011).
16. One-sentence create and test runs.
17. Seller-built images and per-account snapshots. **Milestone B:** a design-partner seller with two tenants.

Agent egress (§12) follows as separate ADRs. Launch follows the public release.

New invariants get property tests:
- concurrent agent runs never exceed the account or tenant limit;
- a workflow's agent spend never exceeds its budget;
- one run per dedupe key;
- no delivery for a non-succeeded run.

## Consequences

- **Every run starts fresh and vanishes.** Nothing persists between runs except outputs under retention, which is the isolation story for sellers.
- **Speed.** Platform overhead is a visible share of a one-minute run. The prototype must show per-stage targets (boot under 1 s, input under 3 s, total under 5 s at p50) before milestone A, or the harness snapshot design is revisited.
- **Model calls dominate the cost.** Sellers bring their own keys, so Gregale's share of a run's total cost is small. Positioning rests on idle-free billing, tenancy and integration, not on runtime price.
- **The 1-hour cap and the 10-minute frozen window** are irrelevant to one-shot runs. They return when sessions are designed.
- **Claude Code-based seller products need credentials per tenant.** Each tenant must bring its own Anthropic or third-party provider credential. This moves per-tenant AI gateway credentials ahead of the rest of the per-tenant connection work for any seller building on the `claude-code` preset.
- **Naming.** Gregale may say in plain text that a run "runs Claude Code" or that the preset has Claude Code preinstalled. No Gregale product, feature, plan or UI name uses the Claude Code or Anthropic names or logos. Sellers building on the Agent SDK follow its branding rules: "Powered by Claude" is allowed; "Claude Code Agent" is not.

## Open questions

1. A no-VM model step for tool-free tasks: one AI gateway call, no harness. Largely answered: workflow `outbound` steps (ADR-489) already call a managed outbound integration, including the AI gateway, with no VM. What remains is documenting it as the tool-free path.
2. A small trial allowance despite paid-only plans.
3. Open-sourcing the run contract and a local runner (`gregale agent run --local`).
4. Whether `result.json` alone or a typed outputs manifest is the long-term step output contract.
5. Written confirmation from Anthropic (its legal page points to sales for authentication questions) on three points:
   - that the relay and AI gateway forwarding the end user's own key meets the "no intermediation" condition;
   - that `claude-code` preset runs for sellers' tenants with tenant-owned keys are permitted;
   - whether a seller's Agent SDK-based agent may run on its own key for its customers. The Agent SDK runs the Claude Code binary, and the SDK page allows powering products for one's own customers.
