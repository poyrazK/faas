# ADR 20261022140000 · Break-glass controls for a compromised secret store

- **Status:** accepted (2026-10-22)
- **Date:** 2026-10-22
- **Related:** ADR 20261022111812 (seal domains), ADR-20261019071733 (deploy
  freezes), ADR-039 (server-side session revocation), ADR-034 (scoped API
  keys), ADR-1431 (AI gateway access tokens),
  `docs/runbooks/breach-notification.md`,
  `docs/runbooks/customer-secret-store-compromise.md`

## Context

On 2026-10-21 Railway disclosed that an attacker had decrypted customer
environment variables
([status timeline](https://status.railway.com/incident/QH8L8B3E)). Within a
day it had paused builds and deploys on the whole platform, invalidated every
API token and OAuth session, rotated every bucket credential, cut GitHub
integration tokens, and resumed builds by plan. It did all of this under
pressure, posting hourly updates that said nothing new.

Gregale had no platform-wide version of any of these controls:

- **Deploy freezes** (ADR-20261019071733) belong to a customer, cover only
  production, and any principal allowed to deploy can override them.
- **Session revocation** exists per account
  (`gregalectl accounts revoke-sessions`) and per key, not for everyone.
- **Builders** claim every queued build. There is no switch that holds them.
- `docs/runbooks/breach-notification.md` says "revoke or rotate exposed API
  keys, session keys, …" without saying how to do it for every account at
  once.

## Decision

### 1. A platform release pause

A runtime configuration key, `platform_release_pause_enabled` (boolean, hot,
category Security), pauses every release on the platform:

- **apid** refuses every new deployment and every new schedule with 503
  `platform_release_paused`. The check sits at the top of
  `releaseFreezeProblem` and `releaseFreezeOrSchedule`, which every creation
  path calls: the deployment API, source and upload deploys, git-ref and
  GitHub push deploys, project image deploys, and promotions. Unlike a
  freeze, it covers every scope and nothing in the request overrides it.
- **apid's scheduled-release worker** starts no due schedule while paused.
  Schedules are decided on the first pass after the pause is lifted.
- **builderd** claims no queued build: the notification path leaves it
  queued, and the polling path answers as if the queue were empty. The
  polling worker picks the builds up after the pause is lifted.
- **Retries and GitOps environment syncs** are paused too (fixed on
  2026-10-24): a retried image deployment and an environment candidate built
  from an image or an existing artifact need no builder, so builderd's pause
  did not hold them. A paused GitOps sync reports itself unconverged and
  continues after the pause is lifted.
- **Not paused:** rollbacks (they do not pass the freeze check), rollouts
  already in progress (as with freezes, a half-finished canary must not be
  left in place), running apps, config and secret writes. Customers must be
  able to rotate their secrets during the incident.

apid follows the key through its runtime configuration subscriber, and
builderd through a `runtimeconfig.Watcher`. A daemon that cannot read the key
does not pause, so a database outage cannot hold builds forever. Without a
database, apid cannot create deployments anyway.

### 2. Platform-wide credential revocation

`gregalectl break-glass revoke-credentials` revokes every live control-plane
credential of the chosen kinds:

- sessions (dashboard and CLI);
- API keys, including keys in their rotation grace period;
- app deploy tokens, including tokens in their rotation grace period;
- hosting automation credentials, whose rows are deleted, as their own
  revoke does;
- AI gateway access tokens (ADR-1431), which spend customers' provider keys;
- platform tenant access tokens.

Deploy tokens and hosting automation credentials were added on 2026-10-24.
They live up to a year and 30 days, and the first version missed them.
Short-lived credentials stay out: OIDC-exchanged bearers (5 minutes),
dev-bridge sessions (1 hour) and fork access tokens (4 hours at most).

All kinds are revoked in one transaction, which also writes one
`break_glass.credentials_revoked` audit event with the counts and the reason.
Every verifier already checks `revoked_at` or `status`, so revoked
credentials stop working immediately, with no cache to drain. Without `--yes`
the command only counts what it would revoke. Customer application
credentials (consumer keys, realtime and data credentials) are left alone:
revoking them would take customers' own apps down, and they are rotated per
customer.

### 3. The controls write PostgreSQL directly

`gregalectl break-glass pause|resume|status|revoke-credentials` runs on a
control-plane node with the database credentials (`--db-env`), as
`gregalectl fleet-seal` does. They do not go through apid's admin API:

- apid may be the thing that was compromised;
- apid may be down;
- revoking credentials would end the operator session the request rides on.

`pause` and `resume` write the runtime configuration row with a reason, which
also lands in the key's revision history and in a
`break_glass.release_pause_set` audit event.

### 4. A runbook for a compromised secret store

`docs/runbooks/customer-secret-store-compromise.md` gives the order: contain
(pause, revoke), scope (which seal domains the compromised unit or node
held), rotate (customer values first, then platform-issued credentials and
identities), communicate, and resume. `breach-notification.md` links to it.

## Consequences

- **Fast containment.** Two commands contain an incident in minutes, from a
  node, without apid.
- **Revocation is broad on purpose.** Everyone, operators included, signs in
  again, and CI pipelines need new API keys. The dry run shows the counts
  before anyone commits to that.
- **The pause has no per-plan staging.** Railway resumed Enterprise first.
  Resuming by plan needs the pause to carry a set of exempt plans. That is a
  later change.
- **Customers see a 503 with a stable code,** which the CLI and SDKs already
  treat as retryable.

## Rejected alternatives

- **A platform-wide deploy freeze.** Freezes are customer data, cover only
  production, and are meant to be overridable.
- **An epoch or "not before" cut-off for every credential.** It would need a
  check in every verifier and a new column or setting each of them reads.
  ADR-039 already rejected account-wide epochs. A bulk update of `revoked_at`
  uses the checks every verifier already has.
- **Break-glass through apid's admin API.** See decision 3.

## Not covered here

Forced fleet-wide rotation of platform-issued credentials (object storage
bindings, managed PostgreSQL passwords, GitHub install tokens), notifications
targeted by what each account stored, resuming releases by plan, and a status
page that does not depend on apid (ADR-130 serves it from apid) are follow-ups.
