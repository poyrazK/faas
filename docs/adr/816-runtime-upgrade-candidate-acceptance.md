# ADR-816: Fresh cold-boot readiness receipts for runtime upgrade candidates

Status: accepted · 2026-10-06

## Context

ADRs 597–601 retain exact runtime/source targets, reviewed serving/configuration
baselines and operator native qualification. They do not prove that an upgrade
candidate's rebuilt application became ready. A successful snapshot restore,
release qualification fixture or historical health observation cannot establish
fresh candidate cold-boot readiness.

## Decision

Reuse schedd's deployment prime, which admits a fresh COLD_BOOTING instance and
calls vmmd CreateColdBoot. Before admission, require the exact upgrade target,
an unchanged retained baseline, unrevoked native release qualification and an
explicit zero-traffic candidate. Reject Job artifact-only prime, canaries and
service rollout candidates. Ordinary deployment prime keeps its existing behavior.

Compare the prepared base/layer with the exact pinned release and candidate
layer. Require the cold-boot response to name the original admitted instance,
report COLD_BOOT and contain no restore fallback. vmmd's existing cold-boot
contract returns only after guest readiness; no snapshot or second VM call is
introduced. On a mismatch or rejected publication, use the existing failed
prime cleanup and release admission.

Schedd supplies private cold-boot evidence to PublishOwnedInstanceRuntime.
Commit an immutable `runtime-upgrade-prime-v1` receipt in the same transaction
as COLD_BOOTING → RUNNING, the original config/secret proof and runtime inputs.
Only this publication creates a receipt; the read seam has no independent
record method or customer mutation endpoint. Ordinary wake/restore, graph
qualification and recovery publication cannot manufacture candidate acceptance.

Bind deployment, runtime release, physical layer, original instance/node/wake,
complete guest configuration and secret-version fingerprints, qualification
report digest, cold-boot dispatch time and readiness publication time. Retain
identities and fingerprints only, never plaintext or sealed secret values.
Runtime release identity covers the exact base bytes independently of the
layer's account binding. This is a trusted scheduler acknowledgment of vmmd's
readiness contract, not a new signed native-host journal or retirement receipt.

Publication retains the original environment/app/candidate configuration
locks, a share lock on the serving deployment and its workload pin, and a share
lock on release qualification through commit. Revocation cannot interleave
between the qualification check and receipt publication. MemStore uses the same
checks under its mutex. Failed receipt insertion rolls back runtime publication;
a conflicting second prime cannot overwrite or refresh the original evidence.

Provide a read-only validator using one repeatable-read PostgreSQL transaction
or one MemStore mutex. Check the current retained baseline, candidate layer's
runtime binding, guest configuration/secret fingerprints, qualification digest
and revocation, deployment status, held traffic and freshness. Missing receipts
remain distinct from invalidated receipts and infrastructure read errors never
select a weaker path.

The central `api.RuntimeUpgradeAcceptanceMaxAge` policy is 15 minutes from
cold-boot dispatch, including boot and pipeline time. Time is authored by trusted
schedd/state clocks; this does not add a guest-clock or native-host attestation.
There is no refresh operation. A new candidate attempt must cold-boot again.
Retries retain target and review baseline but never copy acceptance. Environment
cloning classifies receipts as operational and excludes them. Instance cleanup
does not erase historical evidence; deletion of the candidate cascades it.

## Consequences

The receipt proves candidate cold-boot/readiness at publication. Later snapshot,
hosting verification or rollout failures remain governed by their existing
pipeline; this receipt does not claim their success or prove native retirement.
Publication precedes ready/snapshot notifications, so notification recovery does
not create a new receipt or extend its lifetime.

Read validation is preparation evidence only. Customer apply remains
`execution_available=false`; no traffic endpoint, maintenance scheduler or
automatic upgrade is enabled. The next slice must introduce authoritative apid
cutover, repeat every receipt/baseline/qualification check in its traffic write
transaction, serialize with revocation/configuration changes, and prevent
generic traffic redistribution or rollout writers from bypassing that gate.
Existing generic traffic writers are not claimed to enforce this new receipt.

## Validation

Memory and real PostgreSQL tests cover atomic readiness publication, exact
artifact/attempt identity, forbidden restore evidence, input/artifact drift,
qualification revocation, concurrent publication/revocation, failed candidates,
immutable SQL receipts, parent cleanup and retry isolation. Scheduler tests
exercise the actual Prime path, proof-before-notification, invalid cold-boot
responses, pre-admission refusals and drift during boot. Unit observations are
synthetic and never qualify a runtime. VM lifecycle deployment acceptance still
requires designated native Linux amd64 test-metal and final leakcheck.
