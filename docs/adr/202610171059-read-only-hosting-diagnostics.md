# ADR-202610171059: Read-only frontend deployment diagnostics

Date: 2026-10-17
Status: Accepted for the internal default-off pilot

## Context

Frontend hosting has durable CI intent, private previews, release verification,
publication receipts, explicit retirement, retention and OIDC. An interrupted
job currently leaves customers to correlate IDs and separate resource views.
A retry must not extend its original preview window or allocate replacements
for removed resources. A provider publication receipt does not prove serving
health. Live provider qualification remains deferred.

## Decision

Add a separately flagged operator-only `GET /v1/internal/hosting/sites/{site}/diagnostics`
with one optional canonical `run_id` UUID query parameter. It composes existing
site, routing snapshot, run, release and preview reads; no new storage schema,
allocation, upload grant, finalize, promotion, retry, GC or provider operation.
The read contract has no mutation, credential, trust or account usage methods.
A default-off hot API flag and independent CLI/dashboard flags control access.
Capabilities advertise availability; provider Terraform leaves it disabled.

One pure classifier produces closed, bounded checks for site intent, selected
release verification, original CI retry window, preview lifecycle, publication
and unchecked serving health. It returns only resource IDs, manifest digest,
observation timestamps, classified guidance, generations and recorded next retry
time. It never returns manifests, filenames, source labels, hostnames, URLs,
provider error payloads, origin hashes, tokens or credentials. Unrecognized
provider errors become fixed text. Report production is not a health assertion.

Storage errors abort the report with the existing generic problem response.
Only a successful not-found read can classify a record as absent. Disabled
feature reads are explicitly unavailable; they are never treated as removed
resources. A missing run remains unknown, not permission to allocate a new one.
Expired, revoked, retiring, absent and inconsistent resources require inspection.
A usable incomplete run suggests retrying only the original unchanged intent;
the server remains responsible for revalidating all mutations and fingerprints.

Reads are separate observations, not an atomic transaction. Re-read site and
routing intent, and the CI preview-created marker after preview reads. Detected
changes require refresh. This detects common races but does not establish a
permanent coherent snapshot of every record. Every client says to refresh
before taking an action. The diagnostics switch is checked again after reads.
No diagnostic request probes DNS, TLS, Access, GitHub, R2 or the public URL.

A site automation bearer may GET this exact endpoint for its own site only when
it has both stage and preview operations. This aggregates data it can already
read; no other account, trust, management or mutation endpoint is granted. OIDC
credentials retain their existing automation and OIDC kill switches. The feature
flag and normal operator/account/MFA/scope/ownership controls apply independently.

CLI `sites diagnose` accepts optional original `--run-id` and `--path`/`--spa`.
It uses a private no-redirect API client; optional explicit `--oidc-trust` reuses
the existing short-lived GitHub exchange path. This optional authentication
contacts GitHub and mints/renews authentication sessions, independently of the
read-only diagnostic API. Local comparison hashes files with
the existing static-manifest validation and compares the retained manifest digest.
Changed output blocks a retry suggestion. A match never clears another blocker
and does not validate source labels or TTL. JSON reports exclude local paths and
file lists. Exit 0 means a report was produced; callers must inspect checks and
retry advice, not use the exit code as a health/readiness gate.

The dashboard displays site or explicitly selected CI intent diagnostics, with
read-only refresh and no recovery mutation buttons. Selecting a different run
mounts a separate report and aborts obsolete requests. Failed refreshes hide old
recommendations rather than leaving stale recovery advice visible. Invalid run
IDs never reach the API. No background listing of all retained CI run identities
or expansion of their retained limits is introduced.

## Validation

Classifier tests cover pending verification, missing and retiring releases,
expired runs, missing/revoked previews, disabled reads, generations, missing
receipts, unrecognized errors, consistency and redaction. Handler tests prove
no release/preview/run/publication changes and no provider calls; store failures
cannot become healthy or missing evidence. Real routes test default-off/hot
flags and narrow bearer authority. CLI tests prove GET-only behavior, stable
IDs, local mismatch and disabled/invalid requests. Browser fixtures exercise
selected-run isolation, stale failures, blocked guidance and independent flags.
All checks can run without provider credentials. Live serving qualification is
still required before a production readiness claim.
