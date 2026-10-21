# ADR-20261019120020: Atomic project secret snapshots

## Status

Implemented behind `FAAS_PROJECT_RELEASE_SECRETS_ENABLED=1`. Admission defaults
off until the native secret-release lane is qualified. No production deployment
or native execution was performed in the managed workspace.

## Context

Code and non-secret variables already have immutable deployment snapshots.
Application secrets still use mutable scoped rows, so changing credentials
separately can break either the old or new code and a graph rollback cannot
restore the previous credential delivery.

## Decision

The first milestone supports existing application-owned persistent production
secret keys in complete HTTP projects. Managed credentials, ephemeral secrets,
workers, migrations, sidecars, explicit secret grants and reload hooks are refused.
The local CLI accepts `--release-secrets-file` with `--project --atomic --verify
--yes`. The file maps workload names to complete proposed secret value maps.
Unmentioned workloads inherit their prior snapshot; an empty workload map removes
its serving secrets. Adding new key names remains outside this milestone.

The CLI excludes the secret file from packed source and sends values separately
through authenticated TLS. APID requires MFA and secrets write permission, validates
the shared 64 KiB document bound and plan limits, and encrypts values with the
existing host age recipient before persistence. The staging request fingerprint
is a private host-keyed HMAC of canonical input. Plaintext, raw decoder input and
encrypted material never enter public receipts, local journals or diagnostics.
Staging returns random immutable version IDs. The printed request ID permits
same-input replay within the 24-hour staging window; changed input conflicts.
Staging is bounded to 32 live bundles per project; expired unreferenced staging
material is pruned on subsequent staging. It never changes production intent.

The configuration request carries only workload/key/version references. Ownership
checks bind each reference to the same account, project, app and key. Secret
snapshots are included in scan/apply policy identity and source submission guards.
APID persists encrypted private captures and pins the original baseline delivery
before creating candidates. Candidate creation, secret/environment pins and its
acceptance receipt share one transaction. Complete empty snapshots are distinct
from missing pins. Public configuration proofs name candidate and previous version
IDs, without value hashes or ciphertext.

Runtime values read the pin with the deployment owner in one PostgreSQL statement.
Publication fences bind immutable IDs and encrypted material. Deployment-aware
receipt freshness verifies its captured variables and secret selection during
snapshot/wake recovery. Pinned delivery does not update mutable secret-row delivery
observations; runtime receipts and the existing delivery audit identify the process.
Subsequent atomic and ordinary production deployments inherit the active graph's
pins. Graph cutover and paired configuration publication select the corresponding
secret snapshots together. Rollback selects the original deployment snapshots;
PostgreSQL may create a new graph identity around the retained members.

Mutable secret writes, deletion and resealing are rejected while an active graph
owns secret pins. The database trigger takes the app mutation lock before checking
ownership, and memory mirrors the rule. API responses direct callers to an atomic
secret release. Configuration changes retain existing conflict and ABA fences.
Environment transfers remain blocked by the paired deployment snapshot guard.
Operational staging and encrypted configuration pins are registered in clone policy.
Schema downgrade refuses to discard persisted pins.

## Consequences and qualification

This is a release-scoped credential rotation contract. It does not update managed
providers or revoke external credentials. Operators must keep old external
credentials valid throughout the rollback window. The secret registry remains the
mutable legacy source; release receipts identify the effective immutable versions.
Pinned applications must change serving secrets through another atomic release.

[ADR-20261019120021](20261019120021-release-secret-envelope-rotation.md) adds snapshot-aware envelope
rotation and release-key retirement checks. Production enablement requires the
strengthened native secret-release lane, including cold boot and rollback after
removing the old identity. Other encryption domains keep their own migration and
retirement procedures.

`project-release-secrets-only` requires real encrypted candidate delivery,
5/25/100 cohorts, APID/schedd restarts, compatible critical probes, and graph/config/
secret restoration after regression. The guest reports only whether its credential
matches its artifact generation. A source-bound report requires both scenarios,
final leakcheck and successful runner completion. Unit/storage checks do not qualify
native runtime or snapshot lifecycle; admission remains off pending that evidence.
