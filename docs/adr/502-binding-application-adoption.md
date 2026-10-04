# ADR 502: Version-bound application adoption for managed bindings

Date: 2026-10-03
Status: Accepted

## Context

A passing connection probe and a fresh instance admission timestamp do not
show whether a resident application applied its binding credentials. Gregale
already records separate guest projection/signal outcomes and application
acknowledgements for exact secret delivery versions and workload identities.
Operators need these observations at the binding level and an optional gate
that protects them through the traffic transaction.

## Decision

Project managed PostgreSQL and object-storage adoption through bindings
inventory. PostgreSQL expects its managed connection secret; object storage
expects all six managed settings. A single SQL statement (or one MemStore
lock) reads managed owner metadata, current versions, eligible resident
instances, authorized main/sidecar grants and their reload/application
observations. Catalog selectors are permission-filtered before the read.
Exclude task guests, jobs, mirrors, parked/terminal instances and unauthorized
workloads. Include eligible workloads with no report and secret metadata with
no eligible workload. Export no values, hashes, private owner IDs or raw errors.

Derive separate reload and application counts for workload/secret pairs.
Application receipts are version-bound self-attestations, independently of
probe age. Current-version failed reloads or failed acknowledgements block;
missing, invalid, future, unsupported or stale application evidence blocks.
An older valid guest projection does not erase a newer application receipt.
An unavailable observation read reports incomplete/unknown adoption and a
sanitized inventory warning; default preflight remains compatible.

Add `--require-application-ack` to bindings check and gated traffic promotion.
For every managed binding in the candidate scope, require current receipts
from every eligible authorized resident workload and at least one candidate
resident target. A serving revision cannot satisfy an unobserved candidate.
Keep the existing probe, rotation and timestamp checks; the unsupported probe
waiver does not waive application adoption.

Strict clients use `POST /v1/deployments/{id}/promote-with-application-ack`.
The route forces the policy true, including when the body omits it or says
false. Older servers return 404 before mutation; clients never fall back.
Receipts echo the enforced policy. Extend transaction revision triggers to
reload/application observations, sidecar reload support and deployment secret
grants. MemStore captures those same facts explicitly, including private
reload fields omitted by deployment JSON serialization.

## Consequences

Operators can distinguish connectivity, guest reload and application adoption.
Strict promotion requires an already running candidate with application
acknowledgement support; cold candidates and unsupported workloads block.
The existing delivery service disables main-workload reloads for deployments
with sidecars, so granting managed secrets to that main workload prevents a
strict pass; independently supported sidecar grants are still observed.
ADR-503 supersedes this main-workload restriction by extending the guest and
host reload paths while preserving separate grants and receipts.
These observations do not independently establish readiness, ongoing health
or actual credential use. No VM lifecycle or guest protocol changes are
required. The reversible migration installs fences without rewriting receipts.
