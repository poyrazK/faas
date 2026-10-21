# ADR-20261019120041: Read-only project deployment readiness

## Status

Implemented as a metadata-only control-plane check. It does not change VM
lifecycle or enable any deployment capability awaiting native qualification.

## Context

Project source scans, environment previews and release admission already validate
different parts of deployment intent. Operators need a single report for CI that
collects actionable blockers without uploading a repository, staging credentials,
recording qualification or starting builds.

## Decision

Add `gregale deploy check --project` and the authenticated, deployment-read-scoped
`POST /v1/projects/{slug}/deployment-readiness` endpoint. A local check uses the
existing source-selection policy and repository detector. Committed HEAD is used
where ordinary auto source selection uses HEAD; `--source=worktree` explicitly
selects working changes. The scanner-to-plan conversion is shared with APID.
Only workload metadata, key names and explicitly proposed non-secret configuration
or immutable secret references reach the API. `--secrets-file` contributes key
names; its values are never uploaded or persisted by this command.

The report collects dependency errors, missing runtime keys, app quota, atomic
membership/configuration restrictions, migration ownership, worker opt-in and
consumer compatibility, minimum worker overlap quota, rollback retention, critical
probe configuration and default-off capability gates. It reads effective pinned
runtime inputs for active release members. An explicit variable or secret map
replaces that input set, including an empty map. Logical secret identity excludes
envelope and delivery observations. Configuration values, ciphertext, credentials,
source contents and raw backend errors never appear in the report.

`--from ENV --environment ENV` checks environment promotion without inspecting
local source. It reuses the authoritative promotion preview, immutable workload
health policy validation, release graphs and existing qualification/rollout gates.
Protected environments report the need for approval of the final deployment or
promotion plan. Readiness neither issues nor consumes an approval, and cannot
establish that an independently obtained approval authorizes these source bytes.

Each check has a stable category code, `passed`, `blocked` or `unknown` status,
optional affected workload, public message and remedy. Quota checks expose observed
minimum demand and plan limit. A definite blocker makes the report `blocked`;
otherwise any unavailable required check makes it `unknown`; only a complete
passing set makes it `ready`. CLI exits are respectively 1, 2 and 0. JSON and text
use the same outcome. Unsupported servers, invalid reports and transport failures
produce an unknown report and never fall back to a deployment or source upload.

When required state is readable, a hash binds the proposed intent, account/plan,
logical configuration identities and current graphs. `--expected-state HASH`
reports changed inputs as a blocker. Unavailable state never manufactures a valid
comparison. The hash is an advisory comparison identity, not a deployment plan,
source attestation, retention extension, reservation or approval token. The report
sets `runtime_checked:false`. It does not establish free host memory, external
credential validity, arbitrary SQL compatibility, build success or guest health.
Deployment admission remains authoritative and revalidates current inputs.

Request size is bounded by `ProjectDeploymentCheckMaxBytes`, key inventories by
`ProjectDeploymentCheckMaxKeys`, reports by `ProjectDeploymentCheckMaxChecks`,
and workload/managed inventories by the existing release member limit. A truncated
report includes an unknown check and no comparison hash. The API rejects malformed,
unknown-field and trailing JSON. Readiness creates no project, release, candidate,
task, queue hold, staging bundle, approval or audit intent and executes no probes.
No schema migration or new boot flag is required. Existing capability switches
and native qualification requirements retain their deployment behavior.

## Validation

Memory and PostgreSQL contracts check collected blockers, scoped inputs,
configuration drift, unknown reads and redacted diagnostics. PostgreSQL readiness
also runs through a pool with `default_transaction_read_only=on`. CLI contracts
verify metadata-only requests, local secret-value exclusion, promotion checks
outside source directories, deterministic CI outcomes, inconsistent-report
rejection and older-server behavior. API and generated SDK contracts preserve the
structured report. Existing scan conversion and release verification paths reuse
the extracted functions rather than maintaining parallel validation rules.
