# ADR-1666: Wait for selected APIs before migration builds

## Status

Accepted for local implementation.

## Context

Public frontend URL inputs configure the right endpoint, but Next.js page
prerendering and similar builds can fetch data before the selected API is ready.
Topological admission orders enqueue operations. Compose healthy-dependency
activation gates run after building and do not protect these compile requests.

## Decision

Migration creation accepts optional `build_dependencies`, for example
`{"web":["api"]}`. Only selected, declared HTTP/GraphQL dependencies are valid;
self references, duplicates and unrelated services are rejected. The default is
no build wait. Existing public URL and runtime connection choices are independent.

The saved journal retains these names. Prepare reserves every selected attempt's
deployment and build IDs before enqueue. Source admission receives only trusted
journaled API app/deployment pins; it never resolves a failed admission to an
older serving release. Each production source deployment captures a durable
build dependency gate atomically with its deployment transaction, before its
build can enter the queue. Preview and held environment candidates cannot use it.

Builderd checks the captured gate before source reads, cache lookup, fairness
claim accounting or slot allocation. Each pinned API must belong to the same
creator and project, retain its logical name and scope, be the latest deployment,
and be live with 100 percent traffic. Missing, failed, cancelled, replaced,
parked or foreign targets fail closed with a customer-safe blocker. No network
request or customer-supplied URL is fetched by the control plane.

A pending check atomically projects progress and releases the matching running
claim back to the durable queue. All five notification/poll/fairness/affinity
claim paths exclude deferred gates until their next five-second check. FIFO
admission time remains unchanged; waiting consumes no builder VM slot and cannot
starve an unrelated queued build. The fixed deadline is 30 minutes from deployment
admission, preserved across polls and worker restarts. Cancellation retains the
existing deployment/build cascade and claim fencing. PostgreSQL uses the same
deployment-then-build lock order as cancellation.

Gate targets partition the artifact cache recipe. Railpack injects their digest
into every plan step before dependency cache decisions, invalidating compiled
layers when targets change, including warm builds. Dockerfile builds request
`--no-cache` when gated. Successful guests acknowledge the digest; builderd
refuses older guests that ignore it. Empty gates preserve legacy cache keys and
build behavior. These values are platform-derived IDs, never customer secrets.

Resume restores saved choices and resolves only journaled attempt identities.
Retrying a failed frontend preserves the healthy API's deployment. Explicitly
retrying both failed services reserves new IDs for both. Ordinary source-stage
retry copies the predecessor's pins with a fresh bounded deadline; a changed API
requires review through the migration workflow. Unrelated later source deploys
do not inherit migration waits automatically.

The console offers a separate opt-in per declared API, displays waiting progress,
blocker and deadline after reload, links the app for cancellation, and restores
intent on retry. Waiting builds are accepted work and cannot be redeployed via
Resume until failed or cancelled.

## Coverage and limits

Readiness is checked at build admission. Normal public URLs still follow the
application's public traffic routing, and dependencies can change after admission.
This does not pin individual HTTP requests or guarantee data freshness. Customer
API paths, CORS and authentication remain application concerns. Existing external
URLs are not probed or gated. A dependency digest partitions platform artifacts;
mutable external API data is not automatically a cache input.

Validation covers opt-in boundaries, exact saved targets, PostgreSQL restart,
queue fairness and all claim paths, cancellation fencing, deadlines, full-traffic
readiness, partial retry, cache partitioning, guest acknowledgement, generated SDK
wire contracts and restored console progress. Native KVM acceptance must exercise
API-first Next.js page generation, failed API and frontend-only recovery, plus
warm Railpack and Dockerfile builds with changed API deployments before shipping.
