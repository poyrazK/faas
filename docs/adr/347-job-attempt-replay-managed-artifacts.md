# ADR-347: Job attempt history, linked replay, and managed object evidence

Status: accepted (2026-09-29).

## Context

ADR-346 added per-input runs, bounded parallelism, flexible start windows,
and result manifests. The task row is a mutable dispatch projection, so a
retry erased its previous outcome. A run also still read image, RAM, and base
environment from the job at dispatch. Inline inputs were bounded by the API
request size. Result URIs were customer assertions without a Gregale read
path. Flexible jobs had no dedicated queue and expiry measurements.

## Decision

1. Persist each terminal task attempt in `job_task_attempts`. The database
   trigger records terminal transitions and the consumed claimed attempt when
   boot failure or lease recovery increments the attempt. The current task
   remains the dispatch projection. The attempts API is account-scoped.
2. `replay-failed` creates a new run from failed, timed-out, OOM, and cancelled
   tasks of a terminal run. The new run links to `source_run_id`; each task
   carries `source_task_index`. Creation and selection are one transaction.
   Replay requires the current ready image to have the source image reference
   and resolved digest; a legacy source without a captured digest cannot be
   replayed safely. It preserves the source command, environment, timeout,
   retry, failure policy, and input bindings, and starts a standard run with a
   fresh attempt budget. It does not reuse an expired flexible window.
3. A run captures source image reference, resolved digest, immutable artifact
   key, RAM, and merged environment. For a run submitted while imaged is
   pending, publication binds the matching artifact once. Dispatch uses the
   captured values. Legacy rows fall back to the job definition.
4. A caller may supply `input_manifest_uri` and `input_manifest_sha256` in
   place of inline inputs. The URI is `obj://<app-id>/<bucket-id>/<key>` and
   must resolve to a ready bucket in the caller's account with read access.
   The object is a JSON array of input bindings, capped at 16 MiB, and its
   exact bytes must match the supplied SHA-256. Entry order assigns task
   indexes. Plan task limits and existing uniqueness/field checks still apply.
   The run retains both the source URI/checksum and the canonical input digest.
   The built-in S3 and GCS providers expose streamed reads for this path.
   Missing input objects return 404; malformed or mismatched manifests return
   400.
5. A successful task's `obj://` output artifact can be requested through a
   managed download endpoint. The endpoint checks bucket read access, streams
   the current object to verify size and SHA-256, accounts for the URL, then
   returns a five-minute signed GET URL. `s3://` and `gs://` manifest entries
   remain external references. Verification is performed on each download;
   an object modified after verification may still change before the signed
   URL is used, so clients should compare the downloaded SHA-256 as well.
6. The scheduler emits bounded-cardinality counters for flexible starts,
   expiry, and deferral, plus a queue-duration histogram. Job usage minutes
   retain the run ID and execution class alongside the existing billable
   MB-seconds. Historical job minutes default to standard when their instance
   linkage has aged out. Pricing stays at the current rate while these
   measurements are collected and reviewed.

## Rollout

Apply the attempt, snapshot, and metering migrations before deploying API or
scheduler code. Regenerate the Node and Python SDKs from `api/openapi.yaml`;
update the Go SDK alongside the DTO. Run PostgreSQL, API, scheduler, and SDK
tests. The native Linux KVM job lifecycle and recovery drill remain release
gates. Run `RUN_REGEX='^TestJobsE2E_(RunInputOutputContract|PartialCompletionReplay)$' make test-metal`
and `make leakcheck` on the native host before promoting this contract.
