# ADR-20261019120000: Wait for project deployment configuration completion

Date: 2026-10-12
Status: Accepted

## Context

ADR-20261019120055 exposes per-workload cron activation. A live deployment can still be
rolling out or waiting for activation, but project CLI waits previously exited
successfully at readiness. CI could therefore proceed before requested schedules
were active, or succeed despite a terminal configuration conflict.

## Decision

Waited project deploys poll each accepted deployment through readiness, full
traffic and completed rollout/handoff, then server-managed cron activation.
Workloads progress independently under one overall deadline. Polling uses GETs
only; it never prepares intent, enqueues builds or modifies traffic.

Conflicted/discarded configuration and aborted rollouts produce workload errors
and exit 1. Deadline expiry produces exit 3 and identifies the unfinished phase.
A live deployment waiting on rollout/configuration retains its live status and
configuration diagnostic in the result. Interruption leaves server processing
intact. Existing failure, cancellation and supersession outcomes retain their
precedence. JSON includes the existing per-workload error and configuration;
text includes the configuration reason and hint.

Legacy deployments without diagnostics retain readiness compatibility. Once
managed configuration has been observed, missing subsequent diagnostics cannot
turn into success. Unknown configuration statuses fail closed. `--no-wait`
continues to return immediately with the acceptance/configuration receipt.
No server, wire-schema or migration changes are required.
