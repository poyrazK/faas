# ADR-20261019120024: Destination-aware HTTP service graph promotion

## Status

Implemented behind the default-off `FAAS_PROMOTION_SERVICE_GRAPH_ENABLED=1`
admission flag. Requires ADR-20261019120023 health gating. Native guest qualification is
required before production enablement; no production promotion is performed by
this implementation task.

## Context

ADR-20261019120022 refuses generated service URL variables in pinned configuration and
artifact overrides. Copying those strings could preserve source transport or
endpoints. ADR-20261019120023 pins the entry deployment for private critical probes, but
an unpublished candidate has no ordinary release membership for its downstream
service calls. A healthy frontend could therefore exercise the previous API.

## Decision

The existing `projects environments promote --sync-config --verify` command can
promote complete HTTP graphs with declared service bindings. The destination
owns binding assignments, transport, inbound service caller grants, reliability
policy, secrets, authentication credentials and infrastructure. Source artifacts
and ordinary literal configuration continue to follow ADR-20261019120022.

Generated source variables, manifest values and override keys must exactly match
its frozen binding declarations. Unknown or manually changed platform-prefixed
values fail closed. Required binding keys must also exist at the destination.
Every destination dependency must resolve unambiguously to another member of the
same complete project graph; external services, workers, migrations and GitOps
managed runtime graphs are outside this milestone. Quotas and capture byte
bounds remain those in `pkg/api/limits.go`.

The private immutable capture retains source inputs and additionally contains
resolved destination variables, transformed artifact overrides and a normalized
destination configuration document. It materializes every workload's runtime
variables under `env`, including explicit empty maps. Preview exposes only the
resolved document hash and the existing variable-key changes. The release
configuration hash binds all source, previous target and resolved identities,
so preview tokens and protected-environment approvals authorize this exact result.
Ordinary `config_diff` still compares the source and destination input documents;
`destination_config_hash` identifies the document that will actually be published.

Candidate creation writes the resolved variable pins and override values in the
same transaction as its new owner and workload specification. Publication checks
the original source and destination captures and atomically publishes the complete
graph, destination document, workload settings and feature flags. Verification
and retries compare the resolved destination hash rather than the source hash.
Rollback restores the previous document and graph, whose deployments retain their
own binding settings and variable pins. A newer destination edit blocks overwrite.
Existing captures without the new optional fields retain their original hashes
and behavior; no schema migration or new customer flag is needed.

The internal HTTP service proxy resolves bindings using node-verified caller app
and deployment identity. Its state reader captures the graph and frozen policies
in one PostgreSQL statement (or one memory-store lock). It uses workload names or
unambiguous app slugs, checks same account/project/environment, declaration,
HTTP class, settings hash, target caller grants, method/path scope and transport.
The gateway repeats live tenant ownership and deletion checks before endpoint
lookup, uses the target's frozen protocol settings and wakes only the selected
deployment through the scheduler. A guest header cannot widen the resolved graph.

Before publication, only a fully prepared, live, zero-traffic candidate graph of
a running health-gated promotion is eligible. Its private graph identity is the
promotion UUID, never a row in the public release inventory. Health phase must
be `preflight` or `cutover` and the persisted deadline must remain live. Missing,
failed or expired members refuse service routing; no fallback to production is
allowed. The entry request still requires ADR-847's private challenge and normal
customer authentication. Downstream routing requires no application-specific
header propagation: verified deployment identity selects the candidate peers,
including subsequent hops and cold wakes. Calls originating in that candidate
also stay in its graph when initiated outside a probe.

After publication, the same resolver uses the active or unexpired retained graph.
An explicit release hint must match a graph containing the verified caller.
Without a hint, an active graph containing the caller takes precedence over its
identical retained predecessor after rollback; otherwise ambiguous membership is
refused. Retained baseline probes continue to select baseline peers after cutover.
Private authorization expires independently of worker leases and process restarts.
Admission flag changes do not interrupt already admitted durable operations.

## Validation and rollout

Memory and PostgreSQL tests cover transport rewrite, document/runtime agreement,
private candidate and retained baseline routing, publication replay, rollback,
expired or failed health, incomplete candidates, operator edits and rejection
without health gating. Generated binding validation covers tampered values,
undeclared keys, missing dependencies, external targets, secret collisions,
quotas and artifact overrides. API tests cover admission, verification requirement,
restart, regression recovery and destination edits. Gateway HTTP tests exercise
an entry frontend calling its candidate API, a cold dependency wake, stable
customer traffic and header/tenant rejection.

These checks inject network identity and scheduler behavior; they do not replace
Firecracker acceptance. Extend the ADR-20261019120023 native procedure with the frontend/API
case in `docs/ops/promotion-health-native-acceptance.md`. Enable admission only
after the designated native x86_64 KVM host proves destination secrets and URLs,
candidate-to-candidate health, restart/cold boot, regression rollback and leakcheck.
