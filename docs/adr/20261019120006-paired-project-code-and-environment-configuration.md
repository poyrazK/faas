# ADR-20261019120006: Paired project code and non-secret environment configuration

Date: 2026-10-13
Status: Accepted

## Context

ADR-20261019120004/1005 publish and verify source-only project graphs. Runtime plaintext
variables still read mutable `app_envs`. Updating variables before source cutover
can affect the old release, and restoring a graph alone cannot guarantee the
previous runtime values. Existing environment promotions version non-secret
project metadata and guard its rollback, providing the versioning foundation.

## Decision

`gregale deploy --project --atomic --verify --config-file release.json` requests
`atomic-config-verify`. The file contains an `env` object keyed by existing
workload names. Each declared workload's object completely replaces its
plaintext runtime variable snapshot; `{}` removes those variables. Omitted
workloads inherit their previous deployment snapshot, or current named-production
plaintext variables when the deployment has not been pinned yet. No resource,
route, secret, membership or other workload setting changes are admitted.

The request is bounded by the shared project configuration document size and
existing environment-key/value plan limits. Variable names are validated;
secret-reserved names and existing secret or managed-binding shadows are refused.
The configuration file uses its explicit current bytes independently of source
HEAD/worktree selection. Canonical request identity is bound into scan and apply.

The project configuration document's existing metadata is preserved. Selected
workload variable declarations replace their entries under its reserved `env`
member. Incompatible existing `env` metadata fails validation. The current
configuration version/hash, requested target hash, mutable variable fingerprint,
private-key namespace and workload identity are bound into the safe plan token
and submission fingerprint. Values remain server-side; public receipts and local
journals contain only hashes and configuration version numbers.

After admission registration, APID stages a private immutable configuration
capture under the submission identity. It pins baseline deployment variables,
including original creation timestamps, without advancing the configuration head.
Candidate creation attaches the proposed complete variable snapshot in the same
transaction as the deployment and acceptance receipt, before build notification.
Both runtime variable readers use the pin when present, including explicit empty
arrays. Existing runtime publication/configuration fences read the same snapshot;
no scheduler or VM lifecycle transition is added.

A ready graph checks that configuration and mutable variable intent still match
its capture. Graph publication, appending the new project configuration version,
and recording the verification checkpoint commit together. Configuration counters
protect against an operator writing the same value under a newer version. Every
verification/rollback transition repeats the guard; changed configuration becomes
`conflicted` and is preserved. Rollback atomically republishes the previous graph,
appends its previous configuration document when needed, and stores the restored
configuration version with the restored graph ID. Repeated reconciliation returns
the committed identities without appending duplicate versions.

Subsequent atomic source deploys inherit pinned runtime variables and bind the
current configuration version, even without a new configuration file. Ordinary
new deployments in an exact named scope with a configured active graph also
inherit its pin. Legacy unpinned deployments retain mutable-variable semantics.
Pinned deployments do not consume later `env set` values: use a paired
configuration file to change their runtime variables. Secret delivery continues
through its existing live, separately owned path and is never restored by this
non-secret rollback contract.

A code/configuration source snapshot cannot be transferred by clone until that
workflow captures the new snapshot strategy. ADR-20261019120022 adds configuration-synchronizing
promotion for complete HTTP release graphs. Unsupported transfers fail closed,
and schema coverage explicitly
classifies deployment variable pins as configuration. Operational submission
captures are classified separately. Code-only named-scope admission inherits
its target graph's pinned variables. Existing unconfigured transfers are intact.

## Consequences

A candidate can build and pass readiness with its new variables while the old
release continues with its own values. A critical-probe regression restores both
code and non-secret configuration, including after a cold read or controller
restart. This is the first milestone for literal non-secret variables; resource
settings, routes, workers, database migrations and secret rollback remain outside
its contract.

Apply the migration and update APID and every runtime-variable reader, including
schedd, before exposing this mode through updated CLI/SDKs. Older servers reject
its distinct mode, and the CLI refuses a missing or wrong configuration proof
before apply. Native-host acceptance must verify actual guest values at readiness,
cutover, retained revision access and rollback before production enablement.
