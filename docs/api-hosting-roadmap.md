# Gregale API hosting roadmap

**Status:** working product and engineering plan

**Date:** 2026-09-06

**Audience:** product, developer experience, platform, runtime, edge, data, and
operations contributors

## Decision

Gregale will become the easiest and safest place to take an existing HTTP API
from a repository to production:

> Deploy an existing API without rewriting it. Gregale detects how it runs,
> gives it HTTPS, isolates it in a Firecracker microVM, scales it to zero,
> connects its data, and makes every release observable and reversible.

The product wedge is the combination of:

1. Vercel-level deployment simplicity.
2. Railway/Render-level service and data ergonomics.
3. Fly-level container and framework compatibility.
4. Cloudflare-level API primitives and operational visibility.
5. Gregale's own differentiator: a real microVM boundary, snapshot wake in
   hundreds of milliseconds, and zero resident compute while idle.

"Best" does not mean the most top-level commands or the most control-plane
endpoints. It means the shortest path from code to a healthy API, followed by
the shortest path from an incident to an explanation or rollback.

## Target customer and workload

The first target is a developer or small platform team shipping a stateless
HTTP API in Node.js, Python, Go, or an OCI image. Typical workloads are REST,
GraphQL, gRPC, webhooks, streaming/AI responses, scheduled work, and queue
consumers. They want the application to scale to zero when quiet without giving
up normal framework, package, process, and networking behavior.

The first-class journey must cover a solo developer, a GitHub team with preview
environments, and a production service with a database, a custom domain,
alerts, and a safe rollout. It does not initially optimize for static-site
hosting, stateful disks attached to application VMs, a Kubernetes-compatible
surface, or every language with a native buildpack. Arbitrary Linux/amd64 OCI
images remain the compatibility escape hatch.

## North-star journey

An existing Express, FastAPI, Flask, Django, Hono, NestJS, Gin, or standard Go
HTTP application should need no Gregale-specific code:

```sh
gregale login
cd my-api
gregale dev
gregale deploy
```

The first deployment should explain what Gregale inferred and end with a
verified result:

```text
Detected FastAPI · Python 3.13
Start: uvicorn app:app --host 0.0.0.0 --port $PORT
Health: GET /healthz
Build: cached dependencies restored

✓ Live:     https://my-api.apps.gregale.dev
✓ Verified: GET /healthz → 200 in 42 ms
  Logs:     gregale logs my-api --follow
  Inspect:  gregale open my-api
```

Adding persistence should not involve copying a credential between products:

```sh
gregale add postgres --app my-api --env production
gregale add bucket assets --app my-api --env production
gregale deploy
```

Gregale provisions or binds the resource, writes the sealed application
configuration, verifies connectivity from the runtime network, and displays
names and status without displaying secret values.

## Scorecard

These are release gates, not aspirational dashboard decorations. Measure them
by framework and source shape so one fast happy path cannot hide a broken one.

| Outcome | Product bar | Measurement |
| --- | ---: | --- |
| Time to first healthy API | p50 <= 2 min; p95 <= 5 min from authenticated CLI | clean-account acceptance runs |
| Zero-config compatibility | >= 95% of the maintained framework fixture matrix deploys without Gregale config | required CI matrix |
| Cached edit to live dev URL | p50 <= 5 s; p95 <= 15 s | `gregale dev` telemetry, split by framework |
| Platform-caused deploy success | >= 99.5% | builds excluding customer-code failures |
| Actionable failures | 100% of catalogued failures name cause, relevant evidence, and one next action | error-contract tests |
| Snapshot wake | p50 <= 350 ms; p95 <= 800 ms | reference-node and production histograms |
| Warm gateway overhead | p95 <= 20 ms inside the serving region | gateway minus guest timing |
| Preview readiness | p95 <= 3 min from GitHub event | webhook-to-verified-URL trace |
| Rollback recovery | p95 <= 60 s from command to healthy traffic | production-shaped drill |
| Managed resource binding | p95 <= 2 min with zero plaintext credential exposure | provider qualification suite |
| Availability | Beta >= 99.9%; GA >= 99.95%; 99.99% only after multi-host failover is proven | external probes and error-budget policy |
| Billing confidence | usage view and invoice have zero unexplained delta | hourly shadow and invoice reconciliation |

The north-star product metric is **weekly accounts that reach a verified public
200 response within ten minutes of login**. Guardrails are platform-caused
deploy failure rate, p95 wake latency, support contacts per 100 deployments,
and unexpected invoice adjustments.

## Current position

Gregale has more of the required substrate than its public product currently
communicates.

| Area | Current asset | Gap to a category-leading product |
| --- | --- | --- |
| Isolation and idle cost | Firecracker per workload, snapshot park/wake, plan-aware admission and metering | Prove the headline latency and availability continuously on the production fleet |
| Source deployment | Local working tree, Git ref, tarball, Dockerfile, OCI image, resumable upload, monorepo source roots | Maintain a framework-level compatibility contract instead of only marker-level detection |
| Development loop | `gregale dev` has stable remote URLs, source deltas, BuildKit caches, latest-save-wins, logs, and leases | Make cached edit latency a hard SLO; add framework-aware runtime feedback and a first-class dashboard view |
| Runtimes | Native Node, Python, and Go function runners; Railpack apps; arbitrary full-rootfs OCI fallback | Present "API service" as the primary abstraction and hide function/app/container internals until needed |
| Delivery | GitHub deploys, previews, deployment scopes, rollback, traffic split, mirroring, canary foundations | One opinionated production path with automatic health gates and rollback enabled by default |
| API edge | Custom domains, CORS, route rules, throttling, auth, streaming, gRPC, caching, request budgets | Generate a coherent API policy from an OpenAPI document and show its effective behavior in one place |
| Observability | Logs, metrics, errors, wake timelines, SLOs, traces, deployment comparison, request replay foundations | One request view joining edge, wake, guest, deployment, and upstream timings; useful defaults on every plan |
| Data | BYO-secret integrations, customer object-storage preview, provider-neutral managed-Postgres internals | Customer CLI/API, plan entitlements, qualification, billing, preview isolation, and lifecycle UX |
| Async | Cron, delayed tasks, queues, triggers, jobs, workflows, `waitUntil`, and outbound webhooks | Reduce the vocabulary to a small set of resource bindings and ship end-to-end examples and reliability gates |
| Teams and automation | Scoped keys, organizations, audit, JSON output, SDKs, GitHub Action | Terraform/OpenTofu provider and an explicit safe contract for coding agents |
| Fleet | Multi-node placement and migration work is in progress | Do not promise high availability or global placement until failure drills prove it |

Feature presence in a handler or schema is not availability. Every capability
must have one maturity state: `internal`, `preview`, `beta`, or `ga`. The public
website, CLI capability response, dashboard, SDK docs, and pricing page must all
derive that state and its plan entitlement from the same registry.

## The market bar

The goal is not to clone competitors, but their strongest workflows define the
minimum experience developers expect in 2026:

- [Vercel Functions](https://vercel.com/docs/functions) automatically configure
  supported frameworks, scale to zero, place compute near data, and put function
  metrics in the deployment workflow. Git commits and pull requests create
  deployments through the normal Git integration.
- [Cloudflare Workers framework guides](https://developers.cloudflare.com/workers/framework-guides/)
  explicitly cover API frameworks such as FastAPI and Hono. Its platform makes
  [queues](https://developers.cloudflare.com/queues/),
  [durable workflows](https://developers.cloudflare.com/workflows/), and
  [automatic traces](https://developers.cloudflare.com/workers/observability/)
  feel like bindings rather than separate infrastructure products.
- [Railway PostgreSQL](https://docs.railway.com/databases/postgresql) establishes
  the one-click database bar: provision a database and expose `DATABASE_URL`
  without manual credential wiring.
- [Render web services](https://render.com/docs/web-services) set a clear
  framework-compatibility expectation for Express, Django, FastAPI, Rails, Gin,
  and other ordinary servers, while
  [service previews](https://render.com/docs/service-previews) make an isolated
  URL part of the Git review loop.
- [Fly Launch](https://fly.io/docs/launch/) sets the compatibility and control
  bar for arbitrary applications, process groups, scaling, and regions, while
  [autostop/autostart](https://fly.io/docs/launch/autostop-autostart/) makes the
  idle-cost trade-off explicit.

Gregale can win the intersection: ordinary server compatibility and strong
isolation without requiring the developer to become an infrastructure
operator.

## Product principles

1. **Existing code wins.** Prefer detection and adapters over Gregale-specific
   handler formats. A standard server that binds to `$PORT` is the universal
   contract.
2. **One happy path.** `dev`, `deploy`, `logs`, `open`, `add`, `env`, and
   `rollback` should cover most sessions. Advanced controls use progressive
   disclosure.
3. **Explain every inference.** Detection is visible, overridable, and stored in
   the deployment receipt. No hidden magic that cannot be reproduced.
4. **Safe by default.** TLS, secret scanning, isolated builds, health checks,
   gradual production releases, bounded retries, and rollback are defaults.
5. **Portable standards.** OCI, OpenAPI, W3C Trace Context, OpenTelemetry,
   PostgreSQL URLs, S3 APIs, and ordinary environment variables prevent lock-in.
6. **Production and development share a path.** Developer acceleration may add
   disposable caches, but never a second build or runtime contract.
7. **Errors are actions.** Every failure says what happened, shows the relevant
   observed value, and offers one command or link that moves the user forward.
8. **Billing is observable behavior.** Show cost implications before a change,
   especially warm floors, replicas, data, egress, and preview environments.
9. **No feature without operations.** A launchable feature includes metrics,
   alerts, recovery, deletion, audit, limits, docs, and an acceptance drill.

## Sequenced path

The phases are ordered by customer value and dependency. Work within a phase
can run in parallel, but its exit gate must pass before the next phase becomes a
public promise.

### Phase 0 — Establish product truth

**Outcome:** everyone can tell what Gregale supports, at what maturity, on which
plan, and whether it works today.

1. Add an `api-hosting-contract` test suite with production-shaped fixtures:
   Express, Hono, NestJS, FastAPI, Flask, Django, Go `net/http`, Gin, gRPC,
   streaming/SSE, Dockerfile, arbitrary OCI, and representative npm/pnpm/uv/Go
   workspaces.
2. Run every fixture through detect, build, boot, readiness, public request,
   idle park, snapshot wake, logs, and teardown. Keep the quick subset in CI and
   the Firecracker subset on the reference-node gate.
3. Create one machine-readable capability registry containing maturity,
   entitlement, documentation URL, operator flag, and acceptance test. Generate
   the public feature matrix and dashboard capability response from it. Keep
   numeric quotas in `pkg/api/limits.go`.
4. Instrument login -> source detected -> upload -> build -> verified URL as one
   funnel, with timing and stable failure codes. Never attach source paths,
   repository names, environment values, or customer code to analytics.
5. Reconcile `gregale` versus legacy `faas` naming, prices, domains, runtime
   versions, feature flags, CLI help, OpenAPI, and website copy. Fail CI on
   drift.
6. Split root help into `Core`, `API`, `Data`, `Delivery`, `Observe`, and
   `Advanced`; hide operator-only commands from customer binaries or move them
   to `gregalectl` without breaking existing scripts.

**Exit gate:** the fixture matrix is green; every public capability is `beta` or
`ga`; website, CLI, OpenAPI, SDKs, and limits agree; the activation funnel has a
complete trace for at least 95% of deploy attempts.

### Phase 1 — Make zero-config API deploy the headline

**Outcome:** a normal API repository deploys with one command and no platform
configuration.

1. Introduce versioned framework profiles. Each profile owns marker detection,
   start-command inference, runtime/version choice, package-manager behavior,
   default health probe, required bind address, and targeted remediation copy.
2. Detect common framework failure modes before upload: localhost-only binds,
   hard-coded ports, missing production dependencies, development start
   commands, unsupported native binaries, and absent migration commands.
3. Make `gregale doctor` part of deployment automatically for checks that are
   deterministic and fast. Reserve `--doctor-strict` for policy choices, not
   basic correctness.
4. Print and persist a deployment receipt containing source identity, framework
   profile/version, build plan, run command, port, health check, environment,
   resource profile, URL, and artifact provenance.
5. After readiness, perform a platform-side smoke request and show its result.
   A queued deployment is not a successful deployment.
6. Publish one copy-paste quickstart and one maintained example per framework.
   Every example is deployed nightly through the same public API.
7. Provide explicit overrides in a small `gregale.yaml`, but never require the
   file when inference succeeds.

**Exit gate:** at least 95% of the fixture matrix deploys unchanged; p95 first
healthy API is at most five minutes; every fixture failure maps to actionable
copy; no framework quickstart can drift without failing CI.

### Phase 2 — Make the inner loop the fastest

**Outcome:** developers use Gregale while writing the API, not only after it is
finished.

1. Put the existing `gregale dev` path under the edit-to-live SLO. Report source
   delta, dependency-cache hit, build, boot, readiness, and route-switch timing
   separately so regressions have an owner.
2. Stream build and runtime logs in one ordered view, labeled by deployment and
   source. Keep watching while a build runs and preserve latest-save-wins.
3. Add runtime-aware diagnostics: uncaught startup error, import/module failure,
   wrong bind address, readiness timeout, OOM, crash loop, and failed upstream
   connection should appear next to the edit that caused them.
4. Give every developer a stable isolated environment with branch-scoped
   config, budget, lease, and cleanup. Development and PR preview quotas must
   not unexpectedly consume production capacity.
5. Add `gregale dev --open` and a compact dashboard page showing current source
   sync, active deployment, logs, requests, environment key names, and expiry.
6. Support local service overrides such as a local PostgreSQL URL while keeping
   production bindings sealed and unreachable from the local process.

**Exit gate:** p95 cached edit-to-live is at most 15 seconds for Node, Python,
and Go reference APIs; interrupted sessions resume without a full rebuild when
the cache is valid; configuration and source never cross account/workspace
cache boundaries.

### Phase 3 — Make data and asynchronous work feel built in

**Outcome:** a useful API can gain durable state and background execution
without a second infrastructure project.

1. Graduate provider-neutral managed PostgreSQL from dark capability to a
   customer product only after live provider qualification, usage accounting,
   plan entitlements, invoice mapping, restore promises, deletion, and support
   procedures pass.
2. Expose `gregale add postgres`, `list`, `status`, `connect`, `rotate`, and
   `remove`. Bind `DATABASE_URL` transactionally; never return it after initial
   creation and never print it in the CLI or logs.
3. Graduate object storage behind `gregale add bucket`, using the existing
   private-bucket and signed-URL contract. Complete provider qualification,
   accounting, orphan detection, lifecycle recovery, and customer pricing.
4. Add a provider-neutral Redis/KV binding only after PostgreSQL and object
   storage establish the resource lifecycle pattern. Do not create a generic
   marketplace first.
5. Let development and preview environments request isolated databases/buckets
   with TTLs and explicit seed/migration hooks. Never clone production data by
   default.
6. Present queues, cron, delayed tasks, workflows, jobs, and webhooks as
   bindings in `gregale.yaml` and `gregale add`, backed by the existing APIs.
   The developer should not need to learn six unrelated control-plane nouns.
7. Detect connection storms and recommend or automatically select pooled
   database endpoints suitable for scale-to-zero and concurrency.

**Exit gate:** a new API plus PostgreSQL reaches a verified transaction in less
than ten minutes; credentials never appear in durable plaintext; ambiguous
provider operations recover without duplicates; preview deletion removes or
expires every attached preview resource; usage and invoices reconcile.

### Phase 4 — Make every API production-ready by default

**Outcome:** security, observability, and safe delivery are consequences of
deploying, not a separate setup project.

1. Complete the OpenAPI loop: capture discovered schemas, accept an authoritative
   import, diff deployment versus declared contract, and generate route names,
   hosted docs, request limits, CORS, authentication, and breaking-change gates.
   Applying generated policy must always require an explicit diff/confirmation.
2. Build one request explorer joining request ID, route, status, deployment,
   edge time, queue time, wake phases, guest time, downstream spans, logs, and
   billed dimensions. Keep payload capture off by default and separately
   consented, redacted, size-bounded, and short-lived when enabled.
3. Turn production deploys into a health-gated progression: preview -> smoke ->
   canary -> full traffic. Automatically stop or roll back on bounded error,
   latency, crash-loop, readiness, and schema-regression policies.
4. Make rollback one command and one button, with a measured recovery result
   and an audit record. Never rebuild during rollback.
5. Ship useful alert presets for availability, latency, error rate, OOM,
   deployment regression, certificate health, quota, and spend. Avoid requiring
   customers to understand the platform's internal metrics.
6. Make app authentication, consumer keys, CORS, throttling, body limits,
   request budgets, caching, and static egress policy visible as an effective
   route policy. Show which rule matched a test request before it is deployed.
7. Export logs and traces through OpenTelemetry without trapping customers in
   Gregale's dashboard.

**Exit gate:** a production fixture with a domain, auth, database, OpenAPI
contract, alerts, and canary reaches healthy full traffic in under fifteen
minutes; injected bad releases automatically stop or roll back in drills; a
developer can explain a sampled slow request from one screen or command.

### Phase 5 — Earn the reliability claim

**Outcome:** Gregale is safe for APIs whose owners get paged when they fail.

1. Close the M8 hardening gates before general availability. Publish current
   service status and SLOs without hiding the single-failure-domain phase.
2. Complete multi-host placement, snapshot de-localization, route ownership,
   control-plane HA, and failure-safe migration. Cold boot remains the fallback
   when snapshot recovery is unavailable.
3. Prove host loss, control-plane failover, database failover, object-store
   outage, certificate renewal, regional egress loss, and rollback through
   scheduled production-shaped drills.
4. Add regional placement close to declared data. Multi-region active-active
   becomes a product only after routing, secrets, snapshots, metering, and data
   consistency have explicit contracts.
5. Publish an error-budget policy. Stop feature launches when availability,
   deploy success, or wake latency spends the budget.
6. Offer 99.99% only on a topology that has survived repeated automatic
   failover drills and is monitored externally.

**Exit gate:** loss of any one compute host does not interrupt healthy APIs
beyond the published SLO; control-plane failover preserves idempotent mutations;
monthly drills and restore evidence are fresh; the public status page and
customer SLO views use the same measurements.

### Phase 6 — Make the platform composable and agent-safe

**Outcome:** humans, CI systems, platform teams, and coding agents can all use
the same stable contract safely.

1. Complete the Go, TypeScript, and Python SDK surfaces from the canonical
   OpenAPI document and verify every mutating method supports idempotency.
2. Ship a Terraform/OpenTofu provider for apps, environments, domains, data
   bindings, triggers, policies, and alerts. Secrets use write-only resources.
3. Keep GitHub push deploy and the GitHub Action first class; add OIDC exchange
   so CI does not need a long-lived deploy token.
4. Define an agent contract: capability discovery, JSON/NDJSON output, stable
   RFC 7807 codes, `plan`/`diff` before mutation, idempotency keys, least-privilege
   tokens, bounded waits, and audit attribution.
5. Expose the agent contract through an optional MCP server only after the HTTP
   and CLI contracts are complete. MCP must not become a third behavior path.
6. Publish migration guides from Render, Railway, Fly, Cloud Run, Lambda, and
   Vercel, each exercised against a maintained example repository.

**Exit gate:** the same acceptance scenario can be completed through CLI, SDK,
Terraform, and an authorized coding agent with equivalent diffs, audit events,
and final state.

## First twelve deliverables

These are the recommended issue sequence from the latest `main`. Each should be
small enough to review independently and should name the acceptance test it
adds or closes.

1. Define the machine-readable capability and maturity registry; generate a
   checked-in customer capability matrix.
2. Add the API-hosting fixture catalog and a non-metal detect/build contract
   gate.
3. Add the reference-node boot/request/park/wake suite for that catalog.
4. Introduce versioned framework profiles for Express, Hono, FastAPI, Flask,
   Django, Go `net/http`, and Gin.
5. Add inferred start/port/health fields to the deployment receipt and expose
   them through CLI, API, and dashboard.
6. Make deploy perform and render a post-readiness smoke request.
7. Group customer CLI help and move remaining operator-only entries to
   `gregalectl` while preserving hidden compatibility aliases.
8. Add funnel and phase timing for first deploy and `gregale dev`, with privacy
   and cardinality tests.
9. Build the customer-facing managed-Postgres API/CLI behind an `internal`
   capability state and provider qualification gate.
10. Build the customer-facing object-storage CLI and lifecycle dashboard behind
    a `preview` capability state.
11. Finish the OpenAPI declared-versus-observed diff and route-policy preview as
    one read-only workflow before enabling policy writes.
12. Create the API-hosting GA scorecard and a release gate that reads its
    evidence rather than a manual checklist.

## Architecture and product guardrails

- Preserve the jailer-managed Firecracker VM as the tenant isolation boundary.
- Preserve cold boot as the correctness fallback; snapshots are acceleration,
  not truth.
- Keep numeric plan limits in `pkg/api/limits.go`; the capability registry may
  reference entitlements but must not duplicate quota values.
- Keep `apid` the writer of customer intent, `schedd` the writer of instance
  state, and `vmmd` the root-owned machine boundary.
- A cache may accelerate an existing path but may not create a different build,
  artifact, or runtime contract.
- Managed resources require durable intent, idempotent or discoverable provider
  operations, lease fencing, encrypted credentials, deletion, recovery,
  accounting, and qualification before customer enablement.
- OpenAPI-derived policy is advisory/diff-only until the customer explicitly
  applies it. A schema must never silently change production authentication or
  routing.
- Request bodies, headers, database statements, environment values, and signed
  URLs are sensitive. Do not collect them for product analytics. Debug capture
  is explicit, redacted, bounded, audited, and expires.
- Do not add a new public top-level CLI noun when an existing resource or
  binding can express the workflow.
- Do not market global, highly available, or production-ready behavior ahead of
  the corresponding automated failure drill.

## What not to do next

1. Do not add more isolated edge-rule kinds before the core API deploy, data,
   and diagnosis journeys are coherent.
2. Do not launch a broad marketplace. Make PostgreSQL, object storage, and one
   queue/KV path excellent first.
3. Do not make a Gregale-only application framework. Standard servers,
   containers, OpenAPI, and OpenTelemetry are the moat-friendly choice.
4. Do not expose internal daemon or Firecracker vocabulary in the default
   customer journey.
5. Do not trade the microVM boundary for benchmark wins. Improve snapshot,
   cache, routing, and concurrency paths while preserving the product's reason
   to exist.
6. Do not promise persistent local application files. Productize managed
   external state and make ephemeral disk behavior unmistakable.
7. Do not count merged code as a launched feature. Count verified customer
   outcomes.

## Review cadence

Review this roadmap monthly and after every phase exit. The review owns:

- the scorecard trend and error-budget state;
- capability maturity changes and their evidence;
- the top five deployment failure codes and whether their next action worked;
- framework fixture additions based on real attempted deployments;
- support contacts and abandoned onboarding sessions;
- competitor workflow changes that alter the market bar;
- removal or consolidation of product surface that is not helping activation,
  reliability, or diagnosis.

An item advances from `internal` to `preview`, `beta`, or `ga` only when its
acceptance evidence, operational owner, rollback path, documentation, limits,
and billing behavior are present. A date or a merged pull request is not an exit
gate.
