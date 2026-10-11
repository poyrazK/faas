# ADR-981: Durable admission barriers for coordinated Data API schema updates

- **Status:** Accepted
- **Decision:** Opt-in Data API runtimes use an owner-installed PostgreSQL admission hook and a durable, per-binding-role refresh row. The control schema is private and cannot be exposed through the generated API.

A fresh restart alone makes the new engine available before compatibility, client and authorization verification finish. The startup fingerprint can also become stale after uncoordinated DDL.

Every admitted SQL request takes a shared advisory transaction lock. Owner pause takes the corresponding exclusive lock, waits for admitted transactions, then commits a paused row with a SHA-256 token hash. Requests queued behind pause observe that row before application SQL. The barrier therefore survives process, engine and VM replacement. A competing pause cannot overwrite an existing session.

While paused, a request needs an application JWT and the private refresh token header. The gateway strips caller-provided reserved coordination claims, hashes the header and signs its own internal claims. The hook compares the hash and keeps existing grants/RLS intact. Ordinary requests receive 503. Neither the token nor its hash appears in public status or request logs.

Coordinated sync checks the saved baseline, pauses, migrates, performs a fresh restart, compares generated contracts and exact breaking-change approvals, writes types, then runs client and authorization checks. Resume re-inspects the binding role's live schema and atomically records the verified fingerprint while releasing the barrier. Requests signed by an older runtime are rejected with 409. Missing control state fails closed. The runtime rejects engine-only SIGUSR1 reloads in this mode.

Failure before release keeps the durable pause. `--resume-only` proves ownership and repeats refresh/verification without repeating migrations. An uncertain resume acknowledgement can follow a committed release; the CLI reports that uncertainty instead of asserting rollback or re-pausing another session.

The control reader and SQL engine split the existing two-connection budget. `/healthz` measures engine availability and advertises `refresh_paused`; a healthy paused engine still rejects ordinary data access.

Owners install and manage the control schema using their migration credentials. Public serving credentials cannot modify it. All runtimes sharing a binding must enable the hook and expose the same schemas before coordinated updates begin. Each distinct exposure should use its own binding role. This feature does not fence arbitrary owner/superuser DDL, SQL connections that bypass PostgREST, or disabled/legacy runtimes. Owners must serialize DDL through the refresh workflow. Structural fingerprints do not cover policy bodies or arbitrary function behavior; application authorization checks remain mandatory. Hosted multi-replica qualification requires a staging environment.
