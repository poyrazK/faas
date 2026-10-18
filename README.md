# FaaS

[![ci](https://github.com/poyrazK/faas/actions/workflows/ci.yml/badge.svg)](https://github.com/poyrazK/faas/actions/workflows/ci.yml)
[![CodeQL](https://github.com/poyrazK/faas/actions/workflows/codeql.yml/badge.svg)](https://github.com/poyrazK/faas/security/code-scanning)
[![codecov](https://codecov.io/gh/poyrazK/faas/graph/badge.svg)](https://codecov.io/gh/poyrazK/faas)

> **Proprietary software — not open source.** This is Gregale's startup-owned
> product codebase and is made available for viewing only. Copying,
> modification, distribution, public display, public performance, or use
> requires prior written permission under the [license](LICENSE). External
> contributions are governed by [CONTRIBUTING.md](CONTRIBUTING.md).

FaaS is the codebase behind Gregale, an API-hosting platform for stateless
apps, functions, and async workloads. It turns local source, a GitHub
repository, a Dockerfile, or an OCI image into an isolated
[Firecracker](https://firecracker-microvm.github.io/) microVM with a managed
URL, TLS, logs, metrics, deployment history, and scale-to-zero.

Idle workloads are paused and stored as snapshots instead of consuming
resident memory. The next request is held at the gateway while Gregale restores
the workload. The platform-only wake target and its measurement boundary are
defined in the [implementation whitepaper](docs/faas_implementation_spec.md); a
cold boot from the immutable artifact always remains available when a snapshot
is missing or incompatible.

Feature maturity and plan availability are deliberately not copied into this
README. They are generated from the product registry and published in the
[capability matrix](docs/capabilities.md), so merged code alone is never
presented as a launched product capability.

The external contracts Gregale implements, their supported subsets, and the
evidence behind each compatibility claim are tracked in the generated
[standards matrix](docs/standards.md).

## Quickstart

Install the CLI and launch your project with the guided command:

```bash
curl -fsSL https://get.gregale.dev | sh
gregale start ./my-api
```

The CLI handles login when needed and detects your app. Review the source,
account, connection, access, and resources, then confirm deployment. It uploads
your working files, including uncommitted changes, shows build progress, waits
for readiness, and tests the first response. The result includes the tested URL
and commands to update the app, read logs, open it, and delete it.

```bash
gregale logs my-api --follow
gregale metrics my-api
gregale open my-api
```

No project handy:

```bash
gregale start
```

Run it from an empty directory to choose a Node.js, Python, or Go starter.
Use `gregale deploy` for explicit deployment options and automation.

The npm channel uses the `rc` tag for prereleases. Check the
[installation guide](docs/cli-install.md) for its publication status before
using it:

```bash
npm view gregale dist-tags --json
npm install -g gregale@rc
npx gregale@rc start
```

See the [quickstart](docs/quickstart.md) for the first-deploy flow, the
[installation guide](docs/cli-install.md) for the current platform and release
matrix, the [container compatibility contract](docs/container-compatibility.md)
for direct OCI deployments, and the [generated CLI reference](docs/cli-reference.md)
for commands and flags.

Repository assertions can also run against isolated Gregale instances with
[`gregale test`](docs/scenario-tests.md), including warm, cold-boot, and
snapshot-restore profiles. The [scenario test example](examples/scenario-tests/README.md)
includes a local API, suite manifest, HTML reports, offline report comparison,
and a GitHub Actions workflow.

## Platform areas

The examples below describe the durable product shape rather than an exhaustive
release checklist. Availability and maturity live in the
[capability matrix](docs/capabilities.md).

| Area | Examples |
|---|---|
| Delivery | Framework detection, local source and tarball builds, Dockerfiles, direct container deployments, OCI images, GitHub push-to-deploy, deployment diff/dry-run, monorepo projects, named environments, protected promotions, PR previews, and a remote [`gregale dev`](docs/gregale-dev.md) loop. |
| Runtime | Firecracker isolation, framework apps, functions, versioned runtime profiles, resource profiles, multi-instance routing, scale-to-zero, and bounded disposable executions. |
| Edge | Managed TLS and custom domains, HTTP/1.1, HTTP/2 and gRPC, streaming and SSE, authentication gates, rate limits, edge rules, traffic splitting, canary rollouts, and traffic mirroring. |
| Async | Cron triggers, delayed tasks, event-source mappings, queues, run-to-completion jobs, async invocations, and durable workflows. |
| Data and identity | Environment variables, sealed secrets, scoped API keys, workload identity, private object storage, and a provider-neutral managed PostgreSQL foundation. |
| Operations | Build and deployment stage progress, logs, metrics, alerts, audit history, request exploration, debugger sessions, release summaries, manual rollback, opt-in health-driven rollback, and a thin web dashboard. |

## Deployment model

1. `gregale deploy` or the GitHub integration sends customer intent to `apid`.
   Source detection identifies apps and functions, validates configuration, and
   can preview the affected workloads before changing remote state.
2. Source builds run inside an ephemeral builder microVM. Railpack/BuildKit is
   the zero-config path; Dockerfiles and direct OCI images preserve the
   application's normal process model. Untrusted build code never runs directly
   on the host.
3. `imaged` turns the OCI result into a shared read-only base plus a per-app
   layer, injects `guest-init`, boots the app, waits for readiness, and captures
   a restorable snapshot.
4. `schedd` owns admission and the instance lifecycle. `vmmd` is the only
   privileged daemon and owns Firecracker, jailer, network namespaces, cgroups,
   and snapshot restore.
5. The edge resolves the app, applies policy, wakes it when necessary, and
   proxies the request. Usage and platform health are recorded independently by
   the metering and observability paths.

Functions and framework apps share this data plane. A function is a handler
wrapped by a Gregale runtime; a container app is an HTTP server that follows the
guest listener contract, with no function-framework rewrite required. Both use
the same build, isolation, routing, snapshot, scaling, and metering machinery.

## Architecture

```text
                                      control plane

  CLI / GitHub ──► apid ───────────────► Postgres
                       │                     │
                       ├──► builderd ──► imaged ──► artifact + snapshot
                       │                     │
                       └────────────────► schedd ──► vmmd ──► Firecracker VM
                                                admission     jailer / netns

  client ──TLS──► Caddy / Cloudflare ──► gatewayd-public ──► gatewayd-internal ──► running VM
                                                                                     │
                                                                                     └── cold path ──► schedd / vmmd

  meterd ──► usage and billing       outboundd / realtimed / s3-gatewayd
```

The hot request path is TLS termination → cached route lookup → policy → VM
stream. On a cold request, the internal gateway holds a bounded queue while the
scheduler restores a snapshot or cold-boots the artifact. Performance budgets
and acceptance measurements live in the implementation whitepaper rather than
being copied here.

The reference installation runs on bare-metal x86_64 Linux with KVM. The
platform supports a single-node layout and split control/compute roles; the
architecture also defines per-node scheduling, remote snapshot access,
cross-node routing, migration, and failure recovery. Acceptance state and
outstanding evidence are tracked in [engineering status](docs/STATUS.md), not
duplicated here. The [scale-out plan](docs/scale_out_and_workload_classes.md)
explains the long-term topology.

### Core ownership rules

- Every customer workload runs in a jailer-managed Firecracker microVM; only
  `vmmd` runs privileged.
- `apid` is the only writer of customer intent. `schedd` is the only writer of
  instance state. Daemons coordinate through Postgres notifications and narrow
  gRPC boundaries.
- Snapshots are a cache, never the source of truth. A cold-bootable artifact
  must always exist.
- A parked app consumes zero resident RAM. Live capacity is admitted against a
  per-node RAM and vCPU ledger.
- Restored instances receive distinct network namespaces, jail identities, and
  entropy even when they originate from the same snapshot.

The buildable source of truth is the
[implementation whitepaper](docs/faas_implementation_spec.md). Architectural
changes are recorded in [ADRs](docs/adr/).

## Components

| Component | Responsibility |
|---|---|
| `gatewayd-public` | Plain-HTTP public listener and handoff from the upstream Caddy/Cloudflare edge. |
| `gatewayd-internal` | Routing, request policy, wake coordination, accounting, and proxying. |
| `apid` | Public API, authentication, customer intent, deployment orchestration, and dashboard. |
| `githubd` | GitHub App integration, source-ref deploys, checks, deployments, and PR previews. |
| `builderd` | Durable build queue and isolated Railpack/BuildKit execution. |
| `imaged` | OCI ingestion, rootfs layering, image preparation, and snapshot lifecycle. |
| `schedd` | Admission, placement, wake/park, scaling, eviction, triggers, and async dispatch. |
| `vmmd` | Firecracker/jailer process, VM resources, networking, guest streams, and snapshots. |
| `meterd` | Usage aggregation, quotas, and billing-provider records. |
| `outboundd` | Durable outbound integrations and delivery controls. |
| `realtimed` / `s3-gatewayd` | Managed realtime transport and object-storage data paths. |
| `guest/init` and runners | PID 1, readiness, lifecycle hooks, app launch, and function adapters inside each VM. |

## Sources of truth

Fast-changing facts are kept out of this README and generated from their owning
registries:

| Concern | Authoritative source | Published view |
|---|---|---|
| Feature maturity, entitlement, flags, and acceptance evidence | `pkg/productcap/catalog.json` | [Capability matrix](docs/capabilities.md) |
| External standards, compatibility scope, and evidence | `pkg/productstandards/catalog.json` | [Standards matrix](docs/standards.md) |
| CLI commands, flags, and closed sets | `cmd/gregale/cli_meta.go` | [CLI reference](docs/cli-reference.md), shell completion, and man pages |
| Plan limits and quotas | `pkg/api/limits.go` | [Plans](docs/plans.md) and generated pricing documentation |
| Public API contract | `api/openapi.yaml` | Generated SDKs under `sdk/` |
| Toolchain and dependency versions | `go.mod` and `go.sum` | Go tooling and CI |
| Architecture and invariants | [Implementation whitepaper](docs/faas_implementation_spec.md) and [ADRs](docs/adr/) | This stable overview |
| Landed work and outstanding acceptance evidence | [Engineering status](docs/STATUS.md) | Long-form engineering history and open gates |

Capability promotion requires acceptance evidence, customer documentation,
plan entitlement, operational ownership, and a rollback/recovery procedure in
the same change.

## Repository map

```text
api/          OpenAPI and protobuf contracts
cmd/          one Go binary per daemon, operator tool, and the gregale CLI
pkg/          shared control-plane, runtime, state, gateway, and product packages
guest/        PID 1, guest protocols, executors, and function runners
images/       base, runner, builder, and platform image definitions
deploy/       manifests, Ansible, systemd, networking, observability, and ops
migrations/   append-only PostgreSQL migrations
sdk/          generated client SDKs
docs/         product docs, implementation spec, ADRs, runbooks, and drills
tests/        cross-component fixtures and acceptance support
```

The registries listed above are intentionally not mirrored as hand-maintained
versions or feature lists in this README.

## Development

Use the Go toolchain version declared by `go.mod`; CI uses the same version.

```bash
make build             # build all daemons and CLIs into ./bin
make test              # unit tests; no KVM required
make e2e-general       # real control-plane path with a fake VM boundary
make lint              # Go and repository policy checks
make docs-links-check  # validate customer documentation links

make test-metal        # Firecracker integration tests; KVM + root required
make leakcheck         # assert no leaked netns, TAPs, jails, or cgroups
```

Run the metal and leak gates on a supported bare-metal x86_64 Linux host with
`/dev/kvm` and root. That environment is the production source of truth for VM
and snapshot behavior.

## Documentation

| Document | Purpose |
|---|---|
| [Quickstart](docs/quickstart.md) | First deploy and the essential CLI loop. |
| [CLI reference](docs/cli-reference.md) | Generated command and flag reference. |
| [Capabilities](docs/capabilities.md) | Customer-facing maturity and acceptance evidence. |
| [Standards](docs/standards.md) | External protocol and format compatibility claims. |
| [Implementation whitepaper](docs/faas_implementation_spec.md) | Buildable architecture and invariants. |
| [UX specification](docs/faas_ux_spec.md) | Customer journeys and interaction rules. |
| [API-hosting roadmap](docs/api-hosting-roadmap.md) | Product direction and launch discipline. |
| [Engineering status](docs/STATUS.md) | Milestones, landed work, and remaining gates. |
| [Security](docs/security.md) | Isolation model, controls, and customer responsibilities. |
| [ADRs](docs/adr/) | Architectural decisions and amendments. |
| [Agent guide](CLAUDE.md) | Repository conventions and load-bearing constraints. |
| [License](LICENSE) | Proprietary viewing and usage terms. |
| [Contribution terms](CONTRIBUTING.md) | Rights granted when submitting changes. |

Durable policy changes, worker reason transitions, health incidents and retained recovery evidence can be inspected with `gregale automations history --app APP`, filtered by workflow, run, policy version and time window, or exported one page at a time with `--json`. See [automation authoring](docs/automation-authoring.md#recovery-history) for retention and pagination semantics.

Inspect recovery automation outcomes with
`gregale automations report --app APP --since 7d` (or global `--json`). The report
includes workflow/policy groups, distinct-action blockers and evidence links,
with explicit retention and sample limitations. See
[automation authoring](docs/automation-authoring.md#recovery-effectiveness-report).

Compare proposed recovery settings against retained candidate evidence with
`gregale automations recovery-policy-evaluate --app APP --file POLICY.json --since 7d`.
Terminal failure capture also includes workflows outside active policy filters,
with partial coverage and missing retry checks shown explicitly.
The read-only comparison explains exclusions, unknowns, conditional preparation
limits and recorded execution pressure; historical gaps and replay assumptions are
explicit. See [historical evaluation](docs/automation-authoring.md#historical-recovery-policy-evaluation).

Recovery policy evaluation now includes separately timed `later_eligibility` checks for unreviewed failures. The bounded background observer runs even without a recovery policy, checking retained recovery parents, generation fences and resume blockers. Checks refresh no sooner than five minutes and expire for qualification after 15 minutes. These observations do not change historical preparation totals or authorize retries; missing or stale evidence remains unknown. See [ADR-1026](docs/adr/1026-workflow-failure-eligibility-observer.md).

See [reviewed verification creation helpers](docs/adr/1362-reviewed-verification-creation-helpers.md) for recoverable SDK receipt creation.

See [bounded verification status read retries](docs/adr/1363-bounded-verification-status-read-retries.md) for resilient durable observation.

See [verification run discovery](docs/adr/1364-verification-run-discovery.md) for paginated receipt listings and recovery by retained identity.

See [verification qualification waiting](docs/adr/1365-verification-qualification-waiting.md) for bounded completion after probes succeed.

See [verification write reconciliation](docs/adr/1366-verification-write-reconciliation.md) for recovery after lost admission or completion responses.

See [binding CLI progress](docs/adr/1367-binding-cli-progress.md) for live verification events and retained recovery context.

See [verification creation reconciliation](docs/adr/1368-verification-creation-reconciliation.md) for bounded recovery of the original UUID after an uncertain creation response.

PostgreSQL credential rotation can use the driver-independent SDK
[pool runtime bindings](docs/postgres-pool-runtime.md), with validated adoption and
transaction leases. See the [Node pg.Pool example](examples/postgres-pool-rotation/README.md).
