# ADR-396 · Durable edge certificate evidence

Status: proposed

## Decision

Keep certificate observations separate from customer listener intent. Each record identifies the listener and edge, normalized hostname, exact intent version, observation time, ready state, and certificate expiry. Store no private keys, bundle paths, or provider errors.

Accept publication only for the currently enabled terminating listener with matching hostname and intent timestamp. Replace a per-edge observation only with a strictly newer timestamp. Both memory and PostgreSQL stores enforce this contract. Listener deletion removes its evidence; time-based pruning bounds retained history. Add the table and index in a replay-safe append-only migration and regenerate the matching schema/sqlc artifacts.

Status is per-edge certificate evidence. Disabled, changed, invalid, future-dated, or 60-second-old observations report unknown. Expired certificates or fresh negative evidence report not ready. No observation proves fleet coverage, public reachability, client trust, issuance, or guest availability. Edge identifiers are valid UTF-8, bounded to 128 bytes, and exclude surrounding whitespace and control characters.

This storage foundation does not yet publish from edges or expose customer status. Those integrations follow separately. Linux/amd64 and stateless guest storage remain the container contract.

## Validation

Memory and real PostgreSQL tests cover monotonic replacement, stale intent, multiple edges, pruning, and deletion. Status tests cover expiry, freshness boundaries, policy changes, and invalid evidence. The new migration passes the repository replay gate against real PostgreSQL. Scoped lint and deterministic sqlc regeneration are required before publishing the draft.
