# ADR-20261019120049: Durable deployment submission receipts

Date: 2026-10-11
Status: Accepted

## Context

ADR-20261019120048 saves deploy recovery metadata before submission, but image, multipart,
and source-ref attempts cannot reconnect when the acceptance response is lost.
The HTTP idempotency cache is populated after the handler finishes, expires,
and intentionally reclaims failed/superseded deployments for a fresh retry.
It cannot establish immutable acceptance for a saved attempt.

## Decision

Gregale saves a random 32-character lowercase hexadecimal attempt ID before
submitting and sends it as `Deployment-Attempt-ID` on the canonical deployment,
source-ref, and legacy source-tarball POSTs. This is optional for existing clients.
The ID contains no source, secret, credential, or user-provided retry-key text.

apid validates the header before executing the handler. An accepted attempt is
immutable and scoped to the owned app ID. Both stores create its receipt in the
same transaction/lock as the deployment. Reusing an accepted attempt never
creates another deployment, including after failure or supersession. A concurrent
duplicate is rejected by the store before any second deployment can commit.

The existing Idempotency-Key contract is preserved across fresh attempts. When a
cached success is replayed for another attempt, apid durably attaches that attempt
to the existing owned deployment before writing the response. Failure to attach
fails closed. An attempt receipt takes precedence over stale-cache reclamation.

`GET /v1/apps/{slug}/deployment-submissions/{attempt_id}` uses the existing
deployment-read authentication, MFA and app ownership gates. It returns only
attempt ID, immutable app/deployment IDs and acceptance time. A missing receipt
does not establish rejection: the request may still be running, the server may
be older, or the deployment may have been purged. Resume leaves acceptance
uncertain and never submits again. Terminal status is read from the deployment.

Receipts live for the retained deployment's lifetime and cascade on deployment
or app purge. There is no unbounded independent receipt retention or reaper.
Old saved sessions remain readable and keep their upload-receipt recovery.
Resumable upload commit continues to use its existing durable upload receipt;
project apply and dev-source are outside this increment.

## Consequences and validation

A lost response or process death after admission can be recovered from another
process without the original source files. Source handlers read back their exact
created deployment ID, avoiding a concurrent newer deployment's response.

Acceptance: lose the reply after creation, restart the CLI without source files,
and reconnect to the original deployment with exactly one deployment/build.
Exercise concurrent attempts, failed/superseded originals, cache replay aliases,
missing/unavailable receipts, app replacement, ownership/scopes and PostgreSQL
transaction rollback. Receipt storage failure must never commit a deployment.

ADR-20261019120050 adds a pre-submission policy journal and a server revision fence for
profiling/tracing cleanup. Receipt absence remains insufficient to prove rejection.
