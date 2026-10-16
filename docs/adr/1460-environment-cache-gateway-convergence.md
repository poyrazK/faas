# ADR-1460: Gateway convergence for environment cache policies

Status: Accepted

## Context

ADR-1459 saves isolated environment cache policy and transactional receipts.
Gateways normally refresh desired rules through their 30-second policy cache.
A successful write does not prove that a serving gateway loaded the change.
Agents need a read-only way to verify an exact revision after apply.

## Decision

Track one current environment-cache observer session per registered compute
node, and one acknowledgement per node/app/environment. Begin a new session at
gateway startup. Its process-start timestamp fences delayed registration from
older processes; repeated registration must preserve session identity, start
time and the registry endpoint. Heartbeats use a session compare-and-swap,
so a replaced process cannot refresh or acknowledge its old authority. Endpoint
changes invalidate the session and require a new gateway session.

Run a bounded reconciliation loop from gatewayd-internal. Every five seconds,
refresh the session and read up to 100 unacknowledged policy revisions using a
continuation cursor. Reload immutable environment hosts even without traffic
or notifications. Invalidate all cache keys for that host, compile directly
without the request loader's last-known fallback, and check the loaded exact
revision, compiled rule count, installed cache IDs and cache generation. Record
an acknowledgement only if native desired revision and current session still
match. Policy races, read errors and discarded cache generations never count.
One failed host does not prevent the page's other hosts from being attempted.

An acknowledgement remains valid while its exact desired revision and process
session remain unchanged. The incremental reader skips such acknowledgements;
it does not repeatedly recompile unchanged policy. Observation freshness is
established by node and observer heartbeats within 90 seconds, not by requiring
unchanged policies to be loaded every 90 seconds. New writes and restarts
re-enter the reconciliation queue automatically. Session and observation rows
are bounded by node/policy identities rather than accumulating boot history.

Expose GET `/v1/apps/{slug}/cache-policy/convergence?environment=...` with exact
app/project/environment IDs, desired revision, checked time and aggregate
serving, acknowledged, pending and stale counts. Use the authoritative active
compute-node roster with nonempty ingress endpoints. Converged requires a
positive independent policy revision and every eligible gateway acknowledged
in its current fresh session. Missing sessions, stale/future heartbeats,
changed endpoints and an empty roster are degraded, never vacuous success.
A fresh current session without the exact revision is pending. Fleet names,
addresses, other customers' policy and private rule bodies are not returned.

Add `gregale_watch_environment_cache_policy`. It pins the requested positive
revision, polls for a bounded interval, and reports converged, degraded or
timed_out while observing pending state. Each poll rechecks exact environment
read consent and identity. Superseded revisions return an error; no write or
scope fallback occurs. Timeout preserves the latest pending snapshot and can
be retried. Cancellation and API failures remain errors.

## Consequences

Agents can diagnose, review, apply and verify isolated cache policy. Lost
notifications and idle hosts recover through the durable policy scan. Restarts
cannot reuse previous-session acknowledgements. Large or failing backlogs may
remain pending; serving-policy TTL still operates independently of the watch.
Unnamed single-box gateways have no registered fleet identity and cannot
produce verified convergence. Deploy the migration and updated gateways
before relying on this status. Heartbeat freshness and process-start ordering
assume synchronized native clocks, as does existing fleet monitoring.

This is sampled evidence for registered gateways, not continuous serving
attestation, a guarantee about unregistered processes, cache savings, or native
provider qualification. Session bootstrap retries and pending observations
remain visible rather than being converted into successful propagation.
