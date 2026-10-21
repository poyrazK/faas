# ADR-1240: Management MCP environment consent and real-client CI

## Status

Accepted. Extends ADR-1239.

## Context

The management bridge's initial image workflow covers the legacy default scope.
Registered project environments have independent configuration, immutable release
graphs, qualification, protected-target approval, and durable promotion rollback.
An agent must not gain production writes through a staging consent, or lose its
original execution request after restarting.

## Decision

Expose registered environment release inventory and durable promotion status.
Add expiring promotion and rollback plans using the existing customer API's
preview, promote, and captured-predecessor rollback contracts. Keep image staging
on the default-scope review contract. Do not create configuration, qualification,
or approval mutation tools. Target configuration and secrets remain target-owned.

Browser consent grants default inspection plus separately checked default writes,
named environment reads, and named environment writes. Named grants pin project,
environment, and workload IDs. New environments and workloads require renewed
consent even with account-wide app access. Whole-project operations require every
current workload to be consented in both source and target. ID-only reads and
deployment/instance enumeration enforce environment access. Restricted connections
require a deployment ID for logs and reject API telemetry that aggregates multiple
environments. They retain default health and named release inspection.

Plans pin the account, API, project/environment identities and promotion hash.
Return the read-only promotion preview token so a human can approve the exact
plan through Gregale's ordinary approval endpoint. Accept that human-issued
approval credential only at apply, persist it encrypted with the attempt, and
reuse the original payload and idempotency key on ambiguous retries. No tool can
mint an approval. Existing API authorization and qualification rules remain
authoritative. Persist environment consent and plans in the single-writer journal;
old grants restore with default access only. Revocation removes both plan stores.

Add a management job to MCP contract CI. Run management race tests, build the
actual Gregale CLI, and use an integrity-locked Codex client to discover tools and
call read tools against a fixture. The pinned alpha version provides the needed
experimental app-server tool-call API. Pin upgrades require rerunning acceptance.
No model request, deployment mutation, live account, or secret is needed in CI.

## Consequences

Agents can inspect and release from staging to production using Gregale's native
environment workflow. Named image/source staging, environment-specific telemetry,
live Codex/Claude qualification and shared multi-replica storage remain separate
work. The fixture verifies a real client protocol path, not a live production
account or every desktop client. The durable journal continues to require one
writer and a separate protected encryption key.
