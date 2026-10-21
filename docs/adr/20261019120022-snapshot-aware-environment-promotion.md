# ADR-20261019120022: Snapshot-aware HTTP environment promotion

## Status

Implemented in the promotion storage, API and CLI paths. Native guest acceptance
is required before production rollout; no native VM execution or production
promotion was performed in this workspace.

## Context

Artifact promotion already reuses the tested rootfs and publishes the target
release graph, workload settings and environment configuration atomically.
ADR-20261019120006 deliberately refused configuration-synchronizing transfers of immutable
deployment variables. Removing that refusal alone would leave the promoted code
using destination variables and would lose the previous release's variable set
during rollback.

## Decision

`projects environments promote --sync-config` supports pinned variable snapshots
when both environments have complete active HTTP release graphs. The mode starts
when a source or target member has a variable pin. Existing artifact-only and
unpinned promotion behavior remains available. Workers, jobs, release commands,
sidecars, secret reload hooks and owned work/queue policies are outside this
first milestone.

Generated service URLs stored in deployment variable pins or overrides require a
destination-aware configuration-document rewrite and are refused in this
milestone. Environment-owned manifest bindings and secret references remain at
the destination; staging URLs are never copied as literal production bindings.

The preview binds a private capture of the exact source and previous target
artifacts, runtime variables, desired workload settings, environment configuration
versions and logical secret/binding identities. Explicit configuration-document
variables must match those delivered by the source deployment. The public
`release_configuration_hash` binds those inputs without exposing their values;
`environment_changes` shows variable key names and add/update/remove actions.
The ordinary promotion token and protected-environment approval include this
identity. Source qualification requirements continue to apply.

Operation creation checks the preview identity under the project, environment,
app and deployment locks and saves an immutable private capture with the
promotion. It freezes any legacy destination variable set onto the retained
previous deployment. The capture follows the promotion's lifetime and is
classified as operational state in the clone schema registry. Its size is bounded
to 1 MiB in `pkg/api/limits.go`; workload and environment-key plan limits also
apply. It stores logical secret metadata and hashes, with no sealed secret values.

Each candidate receives a new deployment owner and pins the captured source
variables rebased to the destination scope, including explicit empty arrays.
Source submission, release-attempt and retry metadata never become candidate
ownership. Every workload is prepared, including workloads whose artifact bytes
are unchanged. The existing target secret snapshot is inherited when present;
otherwise secrets continue through the target's live delivery path. Basic-auth
credentials, service binding assignments and assigned infrastructure belong to
the target. Variables cannot shadow destination secrets or binding keys.

Publication repeats the source/target capture check in the graph/configuration
transaction. A newer environment version conflicts even if its contents are
identical. Source graph replacement, changed variables, secret/binding edits,
changed desired settings, incorrect members or altered candidate artifacts
prevent cutover. All candidates must be live and dark and must contain the
captured variables and destination secret identities. PostgreSQL parent locks
serialize writers before this final read, including changes to empty child sets.

The existing verification checks the published graph, candidate ownership,
artifacts, workload settings and configuration receipt. It now also checks the
variable snapshots and target secret/binding identities. Verification failure
uses the existing automatic rollback. Rollback republishes the previous graph,
workload settings and configuration document together; that graph's variable
pins restore its previous runtime values. A later target edit prevents rollback
from overwriting the operator's intent. Later source edits do not prevent
restoring the target. Replays return the committed graph/configuration receipts.

Secret envelope maintenance does not invalidate a saved approval or rollback:
the capture excludes recipients and ciphertext while retaining version counters,
value HMACs, ownership and timestamps. The destination's immutable secret version
IDs survive the transfer and ADR-20261019120021 rekey maintenance.

## Consequences and qualification

Staging can supply the exact tested code and immutable literal variables to
production without rebuilding. Existing deployment readers remain authoritative
for retained releases and cold boot. Once a legacy target has been captured, its
variables follow the immutable-release contract; changing the mutable registry
does not change that retained deployment's runtime variables.

Storage and API tests cover publication, explicit empty snapshots, interrupted
candidate checkpoint recovery, automatic rollback, operator edits, identical-value
version changes, source graph replacement, artifact tampering, target-owned
credentials and secrets, retained secret pins and envelope maintenance. The
PostgreSQL suite also checks private capture immutability and writer exclusion
while publication locks are held.

Before rollout on a dedicated native KVM host, qualify real HTTP guests in both
environments: observe different variable and secret values before promotion;
promote with `--sync-config`; prove unchanged rootfs/digests, source variables and
destination secrets; restart APID/schedd/vmmd and prove cold boot; roll back and
observe the previous variables and destination secret versions again. Retained
release headers must continue selecting their own snapshots. Existing record
verification does not replace application-specific health and smoke probes.
