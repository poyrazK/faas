# ADR-202610171906: Organization-owned managed PostgreSQL

Status: accepted
Date: 2026-10-17

## Context

Company apps and projects survive their creator's departure under ADR-202610171904/202610171905.
Their managed PostgreSQL databases and binding lifecycle still require the
creator's identity. Database credentials must remain usable during offboarding.

## Decision

Persist an immutable organization owner on each database. Preserve database,
binding, provider, credential generation and storage namespace identities.
Backfill ownership from agreeing binding apps, restore ancestry and clone
projects; unbound standalone databases belong to their personal organization.
Reject mixed legacy owners before applying the expansion.

Bindings retain the app storage namespace and separately record the database
storage namespace. The database and app must have the same persisted organization.
Foreign keys for the PostgreSQL catalogue reference resource namespaces.
Creator erasure preserves adopted company databases, credentials, accounting and
operation evidence; personal deletion still requires confirmed provider cleanup.
Organization deletion also requires confirmed deletion of its managed databases.
The organization row serializes deletion against new database reservations.
Accepted company cutovers do not cancel when their former creator requests
personal deletion; their existing durable intent continues under company ownership.

Organization URLs expose the reviewed database and binding lifecycle. Owners and
admins create, restore, configure and delete databases and rotate credentials.
Developers may attach and detach app/environment bindings. Viewers may inspect
metadata; billing seats may read aggregate usage. Credentials are never returned.
Current membership, credential binding, scopes and MFA apply before replay.
Membership fences serialize intent commits with offboarding. Unreviewed cutover
and environment-clone endpoints remain unavailable for company databases.

Plan and usage policy follows the existing permanent organization billing
adoption marker. Quotas and usage aggregate across its database namespaces;
personal inventory, usage and exports exclude adopted company data. Current
company cost summaries include retained provider usage history for its databases.
Ledger storage subjects, provider handles, invoices and customer rates are not
rewritten by this ownership expansion. Shared project manifests resolve only
their own organization's databases.
Manifest attachment requires both the database binding action and a
`postgres:manage`/`admin` credential scope before project mutation.

## Acceptance and rollout

Prove cross-organization isolation, role checks, raced offboarding, aggregate
quota/usage, stable credentials and creator erasure with a remaining teammate.
Remediate conflicting roots and duplicate organization database names before
the migration. Upgrade API writers and background workers before enabling
identity erasure. Old binaries must not purge identities after this cutover.
Native VM deployment acceptance remains a separate release gate.
