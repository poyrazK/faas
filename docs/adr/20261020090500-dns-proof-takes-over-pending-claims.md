# DNS proof takes over another account's pending hostname claim

Date: 2026-10-20
Status: Accepted
Related: ADR-100 (tenant surfaces), ADR-167 (wildcard custom domains), ADR-299 (platform-tenant hostname delegation), migration 20260913200000001 (custom-domain verification deadlines), spec §4.1.2.14

This record has a date-and-time identifier because date-only identifiers
collide under `make adr-number-uniqueness-check` when two ADRs land on the
same day.

## Context

Custom domains (`custom_domains`) and tenant hostnames (`tenant_hostnames`)
are each globally unique by name. Creating either needs no DNS proof: the row
is pending until the DNS poller finds its challenge token at
`_faas-verify.<name>`. A pending row still reserves the name for everyone
else.

- A pending custom domain is exclusive for seven days. After that any
  account may reclaim it, including the account that held it. One account
  can therefore re-claim `shop.example.com` every week, and the domain's
  owner can only get it by racing that account at the deadline.
- A pending tenant hostname never expires, and the per-surface cap
  (`TenantHostnamesPerSurface`) counts verified hostnames only. One surface
  could reserve any number of names, permanently, and every one of them
  joined the poller's queue.

Both are claims that nobody proved. The name's owner proves control the same
way verification already does: by publishing a TXT record.

## Decision

1. **Proof replaces a pending claim.** When a create conflicts with another
   account's unverified row, apid computes the claimant's takeover token,
   `state.ClaimTakeoverToken(kind, account, name)`. It then looks it up at the
   name's normal challenge owner (`state.CustomDomainChallengeName`: the zone
   for a wildcard, `_faas-verify.<name>` otherwise).
   - If the TXT record is present, the store replaces the pending row in one
     statement guarded by `verified_at IS NULL`. The new row is pending with
     the takeover token as its challenge, so the ordinary poller verifies it
     on its next check through the challenge-bound write. The claimant's
     pending-claim quotas apply as for any create.
   - If the record is absent, the response stays the existing 409
     (`validation` "Domain taken" for custom domains,
     `tenant_hostname_already_claimed` for tenant hostnames). Its detail and
     hint now name the TXT record to publish before retrying.

   No status code or error code changes, and the OpenAPI document and SDKs
   are unchanged.
2. **The token is deterministic.** It is a domain-separated SHA-256 over the
   claim kind, the claimant account ID and the lowercased name, truncated to
   32 hex characters. A retry, a second apid replica or a record left in DNS
   from an earlier attempt all yield the same value, so nothing new is
   stored. The token does not need to be secret: only whoever controls the
   name's DNS can publish it. Its only job is to bind the proof to one
   claimant, so the holder's own random challenge never counts as another
   account's proof, and one claimant's record cannot be replayed by another.
3. **Only another account's unverified row can be taken.** A verified row
   (including a verified wildcard, which ADR-167 already protects) and a
   pending row of the claimant's own account keep their 409. A verified row
   whose DNS drifted is unverified again and can be taken by proof. That is
   the case of a domain that changed hands. A tenant hostname left on a
   deleted surface routes nothing, so it is released on proof whatever its
   state or account.
4. **Pending tenant hostnames are capped per surface.**
   `TenantHostnamesPendingPerSurface` (Hobby 10 / Pro 50 / Scale 250, Free 0)
   bounds unverified hostnames on one surface. It is checked under the
   surface lock that already serializes the verified cap. The quota error
   reuses `tenant_hostname_quota` with the pending count as observed.
5. **Each replacement is audited on both accounts.** `domain.claim_taken_over`
   is emitted on the claimant's account and `domain.claim_released` on the
   previous holder's account; tenant hostnames use the `tenant_hostname.`
   prefix.

## Consequences

- Squatting a name no longer blocks its owner. The squatter can re-claim
  after a takeover only by publishing DNS records it does not control. The
  owner's row is verified within a poller tick, and verified rows cannot be
  taken.
- Tenant hostnames still have no verification deadline. With takeover by
  proof and the pending cap, a stale pending hostname costs one row and one
  poll slot, and blocks nobody who can prove the name. Adding a deadline
  would need a migration and a reclaim path that this decision does not
  require.
- The verified cap still counts verified hostnames only, so pending rows
  never block a retry of verification (ADR-100 review finding K). A surface
  at the pending cap must remove a stale pending hostname before it adds
  another.
- A create that conflicts makes one synchronous TXT lookup (3 s timeout).
  Domain creates keep their per-IP rate limit.
- Platform-tenant delegated hostnames (ADR-299) are bounded by the owner's
  delegation policy and keep their existing conflict behavior.

## Rejected alternatives

- **Several pending claims per name, verified first wins.** This needs a new
  claims table, a primary-key change on `custom_domains` and a migration of
  every reader, to reach the same outcome as a takeover.
- **A random takeover token stored per claimant.** This needs a table and a
  cleanup path. The deterministic token gives the same binding with no state.
- **Keying the token with the session binding key.** Secrecy adds nothing
  when the proof is DNS control, and key rotation would invalidate records
  customers already published.
