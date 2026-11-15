# ADR-20261114163130: Python build and deployment waiters with shared contracts

- **Status:** accepted
- **Date:** 2026-11-14
- **Milestone:** SDK developer and agent integration
- **Related:** [ADR-20261113210904](20261113210904-node-sdk-build-and-deployment-waiters.md), [ADR-20261114004557](20261114004557-python-sdk-async-request-options.md), [ADR-20261114013700](20261114013700-python-sdk-lazy-pagination.md)

## Context

The Node SDK has local build and deployment waiters. Python callers can list
history and apply request policy but still implement polling and distinguish
release holds, approvals, watch completion, rollback, supersession and regression
themselves. Both SDKs need the same read-only observation semantics and bounded
behavior when HTTP or user progress callbacks stall.

## Decision

Add FaaSClient.wait_for_build and wait_for_deployment, inherited by preview clients,
returning generated public BuildResponse and ReleaseTimeline models. Observe only
existing IDs using generated GET route builders and row parsers. Resume by calling
again with the retained resource ID; never submit, approve, promote, roll back or
cancel server work from a waiter. Existing generated callers remain unchanged.

Require immutable BuildWaitOptions or DeploymentWaitOptions with an explicit
positive total timeout in seconds (at most 2147483.647). Capture client settings
at invocation and start the budget when awaited. One monotonic deadline covers
status reads, HTTP retries, bodies, poll delays and awaited progress callbacks;
it overrides client timeouts for status reads. Poll immediately, then use capped
exponential jitter (default 1–5 seconds), with an optional positive max_polls.
HTTP retry attempts count within one status poll.

Use a borrowed HTTPX client snapshot, shared with pagination's snapshot helper,
so a token/base URL rotation applies only to new observations and closing a
waiter does not close the owner's connection pool. Own the entire loop in the
async request machinery's bounded worker scope. Give each status read its own
request-policy scope and body lifetime; progress callbacks do not inherit that
read's retry policy or response observer. Retire late responses and callbacks,
check the stopped flag before further polls, preserve native CancelledError and
its message, and keep HTTP/network/callback exception identity.

Apply Node's authoritative state rules: build succeeded returns; failed and
cancelled stop. Release building, scheduled, rolling_out and watching continue.
Awaiting approval and held stop as blocked by default; callers can keep waiting
within their budget. Failed, cancelled, rolled_back and superseded stop. Release
success requires released phase, live status, positive traffic and no recorded
watch_regressed event. Report not_serving or regressed otherwise. This describes
the selected release, not application health or a promise of full traffic.
Unknown states and invalid/mismatched envelopes fail closed before replacing
retained observations; validate DTO parsing before reporting progress.

WaiterError exposes typed failure code, resource, ID, poll count and the last
valid typed model. WaiterTimeoutError also derives from RequestTimeoutError and
TimeoutError. Progress receives an independent typed model copy plus frozen
WaiterProgress(poll, elapsed_ms), including terminal observations. Retain a
separate model for outcomes; the error last_observed property returns a defensive
copy. This preserves Python generated model usability while isolating callback
mutation; Node's corresponding snapshots remain deeply frozen. Keep exception
messages free of IDs and response data.

Preserve the handwritten module and explicit root/preview exports in generator
inputs. Bring the existing local Node waiter implementation into the same SDK
validation tree and add language-neutral contract fixtures to both public source
mirrors. Its owned-reader transport fix also covers cancellation before body
decoding starts. Do not publish test fixtures as runtime package data.

## Consequences and verification

Developers and agents can submit separately, observe a bounded outcome, surface
intervention requirements, and reconnect by ID without risking resubmission.
Cancellation stops local observation and leaves server operations running.

Common Node/Python cases pin public/preview results, state decisions, progress
sequences, poll caps, HTTP retry accounting, held-release resume, failures,
cancellation, header/body/backoff/callback deadlines, and Problem identity.
Python tests additionally cover real concurrent HTTP accounts and socket close,
late custom transports/callbacks, snapshot ownership, callback policy isolation,
jitter caps, caller compatibility, generator preservation and installed-wheel
runtime/strict typing. Retain Node's existing waiter, transport and packed-package
regressions, and verify deterministic Python regeneration and all source mirrors.
