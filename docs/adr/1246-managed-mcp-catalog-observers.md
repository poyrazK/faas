# ADR-1246: Apid-managed MCP catalog observers

Extends ADR-1244 and ADR-1245. Apid already owns monitor intent and receipts and
runs durable background workers. An opt-in apid worker now initializes and
reads catalog metadata from a role's canonical public HTTPS app origin. It does
not create instances or bypass sched/gateway ownership; ordinary HTTP requests
may wake an app and incur normal billing. Meterd remains a reader of retained
monitor evidence and never probes customer apps or resolves MCP role tokens.

The reviewed role configuration stores enabled/paused intent, interval, timeout,
relative endpoint and an optional default-scope app secret reference. Active
secret binding additionally requires secrets-read/admin authorization. Current
ciphertext is scoped by account, app, scope and key, and decrypted with the host
identity ring for each check. Missing/unreadable credentials never downgrade to
anonymous access. Managed database/object credentials are excluded. Source HTTP
uses the existing dial-time egress guard and disallows proxies and redirects.
Only canonical app origins are accepted, including after ownership/workload
revalidation; internal-only apps cannot opt in.

Runtime metadata and leases are persisted beside each role snapshot. The existing
owning app and monitor transaction locks serialize claims, configuration and
completion. A new UUID fencing token, unexpired lease, active managed intent and
matching reviewed revision are all required to retain a report. Expired workers,
replayed completions and probes from before pause/reconfiguration cannot overwrite
current evidence. Completed receipt, heartbeat, retry state and next check time
commit together. A process crash needs no in-memory recovery: the next worker
reclaims the expired lease. Memory storage follows the same ownership rules as
PostgreSQL and failed edits do not mutate retained nested maps.

Four checks per apid process bound source work. Source and store operations have
separate deadlines within a 45-second lease. Scheduling uses five-second polls,
positive jitter and bounded backoff; ordering by due time prevents completed jobs
from monopolizing batches. The configured cadence is best effort under load.
Collector backlog remains visible as overdue status and ultimately unavailable
catalog evidence. No freshness is manufactured while a worker is dead or failing.

Pause/stop disables the role's required collection. Resume/restart issues a new
reviewed revision and clears current worker evidence while preserving transition
history. Failure reasons are a closed vocabulary, not raw source bodies/errors.
No credential values or lease fencing tokens are returned in status projections.
The append-only migration validates observer/runtime payloads and refuses rollback
while managed observer configuration remains. No alert rule is enabled and no
deployment/traffic action is added.
