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

## Quickstart

Install the CLI, sign in, and deploy from an existing project:

```bash
curl -fsSL https://get.gregale.dev | sh
gregale login

cd my-api
gregale deploy --dry-run
gregale deploy
```

The CLI detects the application shape, packages the source, streams the build,
waits for readiness, verifies the public route, and prints the live URL.

```bash
gregale logs my-api --follow
gregale metrics my-api
gregale open my-api
```

No project handy:

```bash
gregale deploy --template hello-node
```

The CLI is also distributed through npm:

```bash
npm install -g gregale
npx gregale deploy
```

See the [quickstart](docs/quickstart.md) for the first-deploy flow, the
[installation guide](docs/cli-install.md) for the current platform and release
matrix, and the [generated CLI reference](docs/cli-reference.md) for commands
and flags.

## Platform areas

The examples below describe the durable product shape rather than an exhaustive
release checklist. Availability and maturity live in the
[capability matrix](docs/capabilities.md).

| Area | Examples |
|---|---|
| Delivery | Framework detection, local source and tarball builds, Dockerfiles, OCI images, GitHub push-to-deploy, deployment diff/dry-run, monorepo projects, named environments, protected promotions, PR previews, and a remote [`gregale dev`](docs/gregale-dev.md) loop. |
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
   the zero-config path; Dockerfiles are the escape hatch. Untrusted build code
   never runs directly on the host.
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
wrapped by a Gregale runtime; an app is an HTTP server that follows the guest
listener contract. Both use the same build, isolation, routing, snapshot,
scaling, and metering machinery.

## Architecture

```text
                                      control plane

  CLI / GitHub ──► apid ───────────────► Postgres
                       │                     │
                       ├──► builderd ──► imaged ──► artifact + snapshot
                       │                     │
                       └────────────────► schedd ──► vmmd ──► Firecracker VM
                                                admission     jailer / netns

  client ──TLS──► gatewayd-public ──► gatewayd-internal ──► running VM
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
| `gatewayd-public` | Public listener, TLS, domain and edge ingress. |
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
| [Implementation whitepaper](docs/faas_implementation_spec.md) | Buildable architecture and invariants. |
| [UX specification](docs/faas_ux_spec.md) | Customer journeys and interaction rules. |
| [API-hosting roadmap](docs/api-hosting-roadmap.md) | Product direction and launch discipline. |
| [Engineering status](docs/STATUS.md) | Milestones, landed work, and remaining gates. |
| [Security](docs/security.md) | Isolation model, controls, and customer responsibilities. |
| [ADRs](docs/adr/) | Architectural decisions and amendments. |
| [Agent guide](CLAUDE.md) | Repository conventions and load-bearing constraints. |
| [License](LICENSE) | Proprietary viewing and usage terms. |
| [Contribution terms](CONTRIBUTING.md) | Rights granted when submitting changes. |
