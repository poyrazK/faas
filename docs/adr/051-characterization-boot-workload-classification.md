# ADR-051 · Characterization boot: observed workload classification + in-guest port normalization

- **Status:** accepted
- **Date:** 2026-07-31
- **Decision:** Classify a workload by **observing its first cold boot**, not by
  parsing its source and not by booting a second throwaway VM. The first cold
  boot of a new deployment runs in **characterizing mode**: guest-init (already
  PID1, already the app's supervisor) watches what the app binds, whether it
  exits and with what code, and — from *inside* the guest — probes the open port
  at L7. It ships one report to the host over AF_VSOCK **STREAM** and the host
  re-derives the authoritative `workload_class`. The instance is not published
  to the gateway's target set until that report lands.

  The `:8080` host contract does **not** become configurable. When an app binds
  a different port, guest-init **normalizes inside the guest** — `PORT=8080`
  injection first, in-guest DNAT second, a userspace forwarder as the fallback —
  so `pkg/fcvm`'s `waitReady` keeps dialing `:8080` forever and ADR-009's
  identical inner network world is preserved intact.

- **Why:** Two facts, both verified against `main`:

  1. **The platform's entire understanding of a customer app is one TCP dial.**
     `pkg/fcvm/vmm.go:1325` hardcodes
     `net.JoinHostPort(l.HostIP.String(), "8080")`. If that accepts, the app is
     "ready". Nothing knows whether it is an HTTP API, a GraphQL server, a cron
     job or a queue consumer. ADR-050 introduces `apps.workload_class` and can
     populate it from a *declared* hint (a compose `command`, a `Procfile`
     process type), but a hint is not a fact and many repos declare nothing.
  2. **`AppManifest.Port`, `Healthz` and `EffectivePort()` are dead code.**
     Grep returns only tests and the SDK mirror. The struct's own docstring
     claims guest-init uses them for readiness; it does not.
     `ManifestFromConfig` (`pkg/oci/image.go:98`) never sets `Port`, so every
     App-type deploy silently inherits `DefaultAppPort`. A stock Express app
     calling `app.listen(3000)` — or any FastAPI app, since uvicorn's default
     host is `127.0.0.1` — builds green, deploys green, and then fails with
     `guest not ready after 30s`. That is the platform's most common failure and
     its least useful error message.

  Observation is the only classifier with **no language problem**: it reads the
  guest kernel's account of what the process did, which is identical for Go,
  Rust, Ruby, Java, Bun, Deno and anything released next year. There is no
  per-language parser and no framework registry to keep current.

- **Consequences:**
  - **No extra VM and no extra boot.** Characterization gates the cold boot that
    already happens on first deploy. This is deliberate: a separate probe VM
    would run customer startup side effects **twice** (an app that runs database
    migrations on boot would run them twice), would contend for the same
    budget as builder slots, and would need its own netns and egress policy.
    Reusing the real first boot means the characterized workload runs in the
    normal tenant netns and therefore **inherits §11 tenant egress policy by
    construction** — deny 25/465/587, RFC1918, link-local, metadata — with no
    second policy to keep in sync.
  - **New `guest/init/characterize_linux.go`** (`//go:build linux && amd64`,
    mirroring `stateless_advisory_linux.go`). Observes:
    - listening sockets from `/proc/net/tcp` + `/proc/net/tcp6`, filtered to the
      app's process tree by socket-inode ownership;
    - process exit and exit code, from the existing supervisor in
      `guest/init/supervise.go`;
    - outbound established connections (the worker signal).
  - **L7 probes run inside the guest**, against `127.0.0.1:<observed port>`:
    `GET /`, a GraphQL `__schema` introspection POST, gRPC server reflection,
    `GET /openapi.json` / `/swagger.json`, and health paths (`/health`,
    `/healthz`, `/ping`). In-guest probing is what makes a loopback-only bind
    observable at all — the host cannot reach `127.0.0.1` inside the guest.
    A discovered OpenAPI document yields the app's full route list and a
    discovered introspection response its full schema, language-independently.
  - **New vsock port, reusing the ADR-047 numbering line.**
    `VsockCharacterizationPort = 1026`, `VsockCharacterizationMsgType = 3`
    (resume = 1024/1, stateless advisory = 1025/2). **STREAM, not DGRAM** —
    ADR-047 chose DGRAM precisely because a dropped advisory is acceptable; here
    the report is load-bearing for the deploy, so a silent drop is not. Guest
    dials host CID 2, sends the JSON report, waits for a 1-byte ack, retries
    with backoff until the characterization deadline.
  - **The host re-derives the class; the guest's proposal is a hint.**
    guest-init is our trusted PID1, but the report crosses a boundary from a
    VM running customer code, so the host validates the wire shape and applies
    the classification rules itself:

    | Observation | Class |
    |---|---|
    | bound + GraphQL introspection answered | `graphql` |
    | bound + gRPC reflection answered | `grpc` |
    | bound, anything else | `http` |
    | nothing bound, exited 0 | `job` |
    | nothing bound, still running | `worker` |
    | nothing bound, exited non-zero | **deploy failure**, not a class |
    | bound on loopback only | normalized, then as above, with a warning |

  - **`waitReady` becomes `waitCharacterized`.** It resolves on a `:8080`
    accept (server classes) **or** on a report establishing `job` / `worker` —
    which today cannot pass readiness at all, because they never bind. Server
    classes join the gateway target set; `job` and `worker` never do. `worker`
    is additionally exempt from idle reaping — parking a queue consumer breaks
    it — which is the `service`-class carve-out
    `docs/scale_out_and_workload_classes.md` D4 anticipated.
  - **Port normalization is a three-step ladder, in-guest, in this order:**
    1. **Inject `PORT=8080`** into the manifest env before exec. Nearly every
       backend framework honours it; most apps then bind 8080 directly and
       steps 2–3 never fire, at zero cost.
    2. **In-guest DNAT** `8080 → <observed>` when the app bound elsewhere.
       Kernel-level, effectively free. **Prerequisite: confirm netfilter NAT is
       compiled into the guest kernel** — this is a build-time capability check
       at boot, not an assumption.
    3. **Userspace forwarder in guest-init** when NAT is unavailable or the app
       bound loopback-only. DNAT to `127.0.0.1` would require
       `route_localnet=1`, which is a security smell we decline; a splice costs
       a few hundred µs per connection and always works.

    The rule or forwarder lives in guest memory and is therefore **captured by
    the snapshot**, so restored instances inherit normalization for free. Live
    connections do not survive restore, which is fine — restore happens from
    parked, with no live connections by definition.
  - **`AppManifest.Port` and `Healthz` stop being dead.** `EffectivePort()`
    becomes the value `waitCharacterized` and the in-guest normalizer agree on,
    and a discovered health path replaces bare TCP-accept readiness with a real
    probe — a strictly better readiness signal, since "socket open" and "ready
    to serve" are not the same claim.
  - **Failure messages become specific.** `guest not ready after 30s` is
    replaced by, e.g., *"your app is listening on 127.0.0.1:8000 — it must bind
    0.0.0.0"*, or *"exited with code 1 during startup"* plus the last log lines.
    This is the single largest DX win in the ADR and it costs nothing extra.
  - **Observed class overrides ADR-050's scan hint.** A compose file declaring
    a service under `worker:` that in fact binds a port is `http`; the observed
    value wins, and the disagreement emits a warning plus an audit event so the
    customer can see why their declaration was overruled.
  - **No migration.** `apps.workload_class` is created by ADR-050's
    `00074_projects_and_workloads.sql`; this ADR populates it. The raw
    characterization report is stored as an audit event (`app.characterized`)
    rather than a new table — the audit surface is the right home for
    observational data per ADR-035, the same reasoning ADR-047 used to decline a
    `stateless_advisories` table.
  - **Characterization window:** 10 s default, early-exit the moment a bind is
    observed and the L7 probes finish, and bounded by the existing
    `readyTimeout` so deploy latency does not regress. Documented edge: a job
    that runs longer than the window classifies as `worker`; if such an app
    later exits 0 cleanly on its first run it is reclassified to `job`.
  - **New metrics**, §12 naming discipline:
    `guest_characterization_duration_seconds`,
    `guest_characterization_class_total{class}`,
    `guest_port_normalization_total{mode="none|dnat|forward"}`.
  - **Touched surfaces:** `guest/init/{characterize,portnorm}_linux.go` (new),
    `guest/init/supervise.go` (exit-code capture), `pkg/api/characterization.go`
    (wire type), `pkg/fcvm/{vmm,manager}.go` (`waitCharacterized` + host vsock
    listener mirroring `listen_resume_linux.go`), `pkg/imaged` (inject
    `PORT=8080` on the App manifest path), `pkg/sched` (target-set gating +
    `worker` reaper exemption), `pkg/state` (persist observed class).
  - **Metal gate.** Everything here is VM-lifecycle code: `make test-metal` and
    `make leakcheck` are required, and `make metal-lima` is the local loop.
    The arm64/x86_64 caveat applies — Lima green is necessary, not sufficient.

- **Rejected alternatives:**
  - **A separate probe microVM after the build.** The version proposed in
    discussion. Rejected on side effects: booting the app an extra time runs its
    startup work twice, which for any app that migrates a schema or claims a
    lease on boot is destructive. It also duplicates netns + egress policy and
    competes for host capacity. Gating the boot that already happens is strictly
    better and strictly cheaper.
  - **Make `:8080` per-app configurable** (revive `AppManifest.Port` as a live
    host-side value). The obvious fix and the wrong one: the host would need
    per-app routing state at wake, and the identical inner world of ADR-009 —
    every guest `10.0.0.2/30` behind `tap0` — is exactly what lets one snapshot
    restore as N instances. Per-app ports chip directly at snapshot reuse.
    Normalizing inside the guest achieves the same customer outcome and keeps
    the invariant whole.
  - **A framework preset registry** (Express → this, NestJS → that), i.e.
    Vercel's actual approach. It works and it rots: coverage is never complete,
    every unsupported framework is a broken deploy plus a PR, and it fails
    hardest on the case we most want to serve — a language nobody here has seen.
    Presets survive only where observation genuinely cannot help: **build**
    shape (NestJS needs `npm run build` before anything binds), which is
    Railpack's job, not this ADR's.
  - **Static source analysis** for `app.listen` / `@app.route` / decorators.
    The language problem restated, one parser per language per framework per
    version, permanently behind.
  - **Host-side port scanning of the guest.** Cannot see a loopback-only bind —
    the single most common real failure — and needs host access to a guest port
    range, re-introducing exactly the per-app host state ADR-009 forbids.
  - **AF_VSOCK DGRAM, as ADR-047 uses.** Correct there (a dropped advisory is
    acceptable by design), wrong here: this report gates a deploy, so a silent
    drop would strand the instance. STREAM with an ack is the matching choice.
  - **Hard-fail when the app binds the wrong port**, teaching `0.0.0.0:$PORT`
    by refusal. Defensible and rejected: normalization is invisible and works,
    and the build log still carries the warning. Consistent with ADR-047's
    Wave-0 stance that observation should inform rather than block.
  - **Ask the customer to declare the class.** Defeats the goal of ADR-050,
    which is that pointing at a repo is the entire interaction.

## Cross-references

- Blocking facts: `pkg/fcvm/vmm.go:1322-1340` (hardcoded `:8080` readiness),
  `pkg/api/appmanifest.go:42-52` (`EffectivePort`/`Healthz`, currently dead),
  `pkg/oci/image.go:98` (`ManifestFromConfig` never sets `Port`),
  `pkg/imaged/handler.go:677` (the Function path, which *does* set them — the
  asymmetry this ADR closes).
- **ADR-050** — creates `apps.workload_class`; this ADR turns its scan *hint*
  into an observed fact. Phase 4 of
  `docs/repo_decomposition_implementation.md`.
- ADR-009 (identical inner network world — the invariant that forces in-guest
  normalization), ADR-005 (snapshots are cache, not truth), ADR-022 (post-restore
  resume hook — the vsock precedent and port-numbering line), ADR-035 (audit
  taxonomy: observational data lives in audit rows), ADR-047 (guest-side
  observation over vsock; the DGRAM choice this ADR deliberately inverts).
- Spec §4.8/§4.9 (guest runtime contract, the `:8080` promise), §11 (tenant
  egress the characterization boot inherits), §12 (metric naming), §14 (metal
  acceptance gates).
- `docs/scale_out_and_workload_classes.md` D4 (`service` class / reaper
  carve-out, reached here by observation rather than declaration).

## Amendment (2026-10-15): several listeners

When the app owns more than one listener (a private TCP port under ADR-576,
a metrics or admin port), the observed port is the app's expected port
(`AppManifest.EffectivePort()`) if it listens on it. Otherwise it is the first
listener, after a `CharacterizationPreferredPortGrace` (500 ms) wait for the
expected port to appear. Before this, the first row in `/proc/net/tcp{,6}` won.
That row order is kernel hash order, so an app serving HTTP on 8080 and a
private TCP service on 5432 could be characterized as HTTP on 5432 (H10-56).
Readiness then probed the TCP port and every later cold boot timed out.
Snapshot restores do not characterize, so only cold boots of apps that never
bind their expected port pay the grace.

## Amendment (2026-10-22): no probe requests with side effects

Owner decision after production-us Hunt 12 (H12-6). The L7 probes are
requests into the customer's process, so they must not act on it:

- There is no GraphQL probe. It POSTed an introspection query to the app's
  `/`, which a handler that acts on POSTs (a webhook receiver, a queue
  consumer) processes like any other request. `graphql` only refined `http`
  on the host, which still accepts it from older guests. The OpenAPI
  `GET /openapi.json` capture (ADR-122) stays.
- A function's cold boot skips the L7 probes. Its runner hands every request
  to the customer's handler: during a queue worker's deploy prime the handler
  processed two probe requests as jobs. vmmd adds `faas.runner=function`
  (`api.FunctionRunnerBootArg`) to the kernel command line when the wake has a
  runner runtime, and guest-init reports the bind without probing.
- The HTTP/2-preface gRPC probe runs only when the HTTP/1.1 probe got no
  HTTP/1.x answer (H11-5): an HTTP/1 server logged it as a protocol error.

## Amendment (2026-10-22): the observed class moves the boot's config proof

A cold boot publishes its instance with a config proof (ADR-531): the
fingerprint of the deployment's runtime configuration it booted with. The
workload class is part of that configuration, and the class is only known
after the boot, so recording an observed class that differs from the scan
hint left the booting instance's proof stale for good. Every later check
of it failed: a frozen thaw refused it as `config_changed`, the frozen
worker tier stopped it instead of freezing it (ADR-20261019103000), and
warm-pool promotion and the ADR-1740 fast path discarded it. On n2 the first
worker of every new worker app paid for this.

schedd now records the class with `RecordObservedWorkloadClass`. In one
transaction it takes the secret writers' lock order (the environment, then
the app exclusively), checks that the instance's proof still matches the
configuration (nothing changed since the publication), writes the class,
and moves that proof to the fingerprint the configuration gives with the new
class. When something else changed in between, it writes the class and
leaves the proof stale, so a real configuration change is never adopted by
a VM that did not boot with it. The secret fence is unaffected. A
deployment pinned to a project environment workload spec never had the
problem: its fingerprint hashes the spec's settings, not the app row's
class.
