# ADR-520 · Self-hosted TLS for customer custom domains

- **Status:** proposed
- **Date:** 2026-10-03
- **Amends:** spec §4.1 ("TLS and domains"), ADR-024 (custom-domain issuance
  path), ADR-167 (wildcard certificate issuance).
- **Decision:** Customer custom domains get certificates from the platform's
  own public edge, with no CDN in their path. The control plane's Caddy uses
  on-demand TLS for every hostname without a static site: before it loads a
  certificate from storage, obtains one, or renews one, it calls
  `GET http://127.0.0.1:9092/v1/internal/tls/ask?domain=<host>` on
  gatewayd-public's loopback control listener and proceeds only on 200.
  gatewayd-public answers 200 for a verified exact custom domain, or for a
  host below a verified wildcard custom domain that has no exact row of its
  own and fits the wildcard's issuance budget. Customers point their domain
  at a DNS-only target hostname (`FAAS_CUSTOM_DOMAIN_TARGET`, for example
  `edge.gregale.dev`) or, at a zone apex, at the edge's addresses
  (`FAAS_CUSTOM_DOMAIN_ADDRESSES`). The feature is off unless the deploy
  manifest sets `public_edge.custom_domains.mode: on_demand`.

## Context

Domain management (add, TXT ownership proof, verify, doctor, wildcards,
environment-scoped domains, certificate status and webhooks) shipped, but no
deployed path issued a certificate for a customer hostname:

- Production terminates TLS at Caddy with a Cloudflare Origin CA
  certificate for `gregale.dev, *.gregale.dev` only, and the host and
  provider firewalls admit 80/443 from Cloudflare only.
- gatewayd-internal's certmagic engine needs `FAAS_TLS_STORAGE_DIR` and
  `FAAS_TLS_CONTACT_EMAIL`, which no deploy path sets. Even when wired it
  solves DNS-01 by writing TXT records with the operator's own DNS token,
  which cannot reach a customer's zone. Its certificates are written to disk
  and served by nothing (PR #633 removed TLS from gatewayd-public).
- The CNAME target customers were told to use, the apps-domain apex, is
  proxied by Cloudflare; a customer hostname resolving there reaches
  Cloudflare, which does not serve it.

The platform must not depend on Cloudflare for customer domains.

## Decision details

- **Terminator.** Caddy stays the TLS boundary (spec §4.1, ADR-070);
  gatewayd-public stays plain HTTP. Caddy's on-demand TLS with an `ask`
  endpoint is the smallest change that issues per hostname, renews, staples
  OCSP and retries with back-off, using HTTP-01 or TLS-ALPN-01 against the
  edge itself. The configuration is compatible with Caddy 2.6 (Ubuntu
  24.04's package).
- **Permission check** (`pkg/gateway/ondemand_tls_ask.go`). The decision
  reads the same rows routing reads: `DomainByName`, then
  `WildcardDomainForHost`. An unverified exact row denies even below a
  verified wildcard, matching routing precedence. Names in or below the apps
  domain are refused; the edge serves them from static certificates.
  Tenant-surface hostnames (ADR-100) are not admitted yet. Store errors deny.
  Only loopback peers are answered. Caddy asks on every handshake whose
  server name has no certificate in memory, so with 443 open to everyone the
  endpoint bounds its store lookups (`OnDemandTLSAskLookupsPerSecond`, burst
  `OnDemandTLSAskLookupBurst`; past it the answer is `deny_overload` and the
  next handshake asks again) and negative-caches names with no custom-domain
  row for `OnDemandTLSAskNegativeCacheSeconds`. Every decision is counted in
  `gateway_tls_on_demand_ask_total{decision}`.
- **Wildcard budget.** Caddy asks again before every reload and renewal
  (certmagic checks the decision before loading from storage), so the budget
  cannot be an in-memory rate limit: each gatewayd-public restart would
  refuse certificates that already exist. `custom_domain_tls_hosts` records
  each admitted host below a wildcard. A known host is admitted for free; a
  new host is admitted only while fewer than 40 hosts were newly admitted for
  that wildcard in the last 7 days (amendment 1 replaces this with shared
  budgets). 40 stays under Let's Encrypt's
  50 certificates per registered domain per week and caps how many orders one
  wildcard can draw from the shared ACME account. gatewayd-public is the only
  writer. The rows are edge runtime state, not customer intent, and cascade
  away with the wildcard.
- **Certificate status.** In on-demand mode apid owns custom-domain
  certificate status through its existing port-443 probe, and stops emitting
  `domain_verify`, so gatewayd-internal's DNS-01 wildcard minter does not
  also write `failed: cert engine unwired`. Right after verification apid
  probes immediately; that handshake is what asks the edge to issue. A failed
  probe within `api.OnDemandTLSIssuanceGraceSeconds` (15 min) of
  verification reports `pending`, not `failed`, so ACME validation that
  outlasts the 5 s probe does not trigger the failure email.
- **Routing target.** The doctor's `points_to_gregale` check accepts the
  configured target, or A/AAAA answers that all belong to the configured edge
  addresses. That also fixes apex domains, for which Go reports the queried
  name as its own canonical name. A configured target replaces the
  apps-domain apex as the expected CNAME, so a domain still pointing at the
  proxied apex is reported (and, if verified, drifted) until fixed; those
  domains could not get a certificate before either. API responses carry
  `dns_records` (TXT proof, CNAME, A/AAAA alternatives) and the CLI prints
  them.
- **Network.** Enabling the feature opens 80/443 to every source in
  nftables. The platform site keeps answering Cloudflare only: Caddy aborts
  connections to `gregale.dev`/`*.gregale.dev` from any other peer, and the
  Host-based route check means a direct client cannot reach a platform host
  by sending a customer SNI. On customer hosts the client IP is the TCP peer;
  a client-sent `CF-Connecting-IP` is never trusted there.

## Consequences

- A customer domain works end to end without Cloudflare: verify the TXT
  proof, point the name at the target, and the first handshake issues the
  certificate. Renewal is automatic.
- The control plane's public address becomes directly reachable on 80/443.
  Platform hosts stay Cloudflare-only at Caddy, not at the packet filter;
  other site blocks appended to the Caddyfile (for example
  `s3.gregale.dev`) become directly reachable. The provider firewall must
  admit 80/443 from everywhere for the control plane, which the public-beta
  GCP audit (`deploy/gcp/public-beta-policy.json`) forbids by default. The
  production policy declares the dedicated control-plane exception described
  in amendment 3.
- Certificates live in Caddy's storage on the single control-plane edge.
  More than one edge needs shared Caddy storage before it can serve customer
  domains; until then a second edge would race the first for ACME orders.
- Wildcard custom domains get one certificate per subdomain, at most 40 new
  subdomains per registered domain per week (amendment 1). SaaS-scale wildcards need a delegated
  DNS-01 path (`_acme-challenge` CNAME to a platform zone) and a real
  wildcard certificate; that is a follow-up.
- gatewayd-internal's DNS-01 engine is unchanged and still used when on-demand
  mode is off. Tenant surfaces keep their own certificate state machine.

## Rejected alternatives

- **Cloudflare for SaaS custom hostnames.** Keeps the edge Cloudflare-only
  but makes customer domains depend on Cloudflare, which is what this ADR
  removes.
- **Bring TLS back into gatewayd-public with certmagic.** Possible (the
  allowlist and issuer code exist) and would let an issuer wrapper meter real
  issuances only, but it re-merges the TLS edge that ADR-070/PR #633 split
  out, needs shared certificate storage in Postgres, and duplicates what Caddy
  already runs in production.
- **In-memory rate limit in the ask endpoint** (or Caddy 2.6's
  `on_demand_tls interval/burst`). Both also gate reloads and renewals, so a
  restart would deny existing certificates. Caddy deprecated the latter for
  that reason.
- **DNS-01 with the operator's DNS token.** Cannot write to a customer zone.

## Amendment 1: shared new-host budgets and a retry backoff (2026-10-31)

Amendment 4 extends the shared budgets to exact hosts and supersedes the
ask-count retry backoff described here.

Bug hunt 32 (M7) found the per-wildcard budget open to griefing and to
evasion. Anyone can send 40 random server names below a customer's wildcard
in a week and spend its budget, because wildcard DNS resolves every name to
the edge, so a DNS check proves nothing. Sibling wildcards under one
registered domain each had 40, while Let's Encrypt allows that domain 50 in
total. Nothing bounded one tenant across its domains, or the fleet against the
shared ACME account's 300 new orders per 3 hours. And because the ledger
cascaded away with its wildcard, deleting and re-creating a wildcard reset the
budget.

Changes:

- **Admission ledger.** `custom_domain_tls_admissions` records each new host
  with its registered domain (public suffix + 1), owner (the wildcard app's
  organization, else its account) and time. It has no foreign key, so it
  outlives the wildcard; gatewayd-public prunes rows older than 7 days.
  `custom_domain_tls_hosts` still decides which hosts are known (free) and
  still cascades.
- **Three caps on new hosts**, checked under one transaction advisory lock:
  `api.OnDemandTLSRegisteredDomainNewHostsPerWeek` (40) per registered domain
  across sibling wildcards, replacing the per-wildcard budget;
  `api.OnDemandTLSOwnerNewHostsPerWeek` (200) per owner; and
  `api.OnDemandTLSFleetNewHostsPer3h` (150) across the deployment, half of
  Let's Encrypt's per-account order limit, leaving the rest for exact
  domains. Refusals are `deny_wildcard_budget` (label kept),
  `deny_owner_budget` and `deny_fleet_budget`. `FaasCustomDomainTLSFleetBudget`
  warns when the fleet cap refuses hosts.
- **Retry backoff.** Caddy asks once per load, obtain or renewal and then
  holds the certificate, so a host allowed in
  `api.OnDemandTLSHostRetryAllows` (3) separate 30-second slices within
  10 minutes is one whose orders keep failing. A known host costs no budget,
  so without a backoff it drew an order, and a failed authorization, on every
  handshake. The ask endpoint now refuses that host with `deny_backoff` for
  1 hour, doubling on each repeat up to 24 hours. The state is in memory: a
  restart forgets it, which only errs toward allowing.

This bounds the damage griefing can do: a registered domain's budget, never
other tenants' or the fleet's. It does not stop a third party spending the
targeted customer's budget. The opt-in per-wildcard label allowlist (next
change) does.

## Amendment 2: an opt-in host allowlist per wildcard (2026-10-31)

Bug hunt 32 (M7, second half). The shared budgets bound what random server
names below a wildcard can cost the platform, but anyone can still spend the
targeted customer's own budget for the week. A wildcard's owner can now list
the names below it that may hold certificates.

- `custom_domain_tls_allowed_hosts` (wildcard, name) holds the list; a name
  is the part of the host before the wildcard's suffix. apid is the only
  writer (customer intent, `GET`/`PUT`/`DELETE /v1/domains/{domain}/hosts`,
  `gregale domain host set|get|clear`, at most
  `api.CustomDomainTLSAllowedHostsMax` (100) names). Rows cascade with the
  wildcard. The organization actions match the domain's other routes: members
  read, members who deploy configure.
- The ask endpoint checks the list after the wildcard's plan and before
  admission, for known hosts too, so removing a name stops its next reload
  and renewal. An unlisted host is refused with `deny_not_allowlisted` and
  never reaches the budgets. No list admits every name, as before. The
  domain doctor's probe host (`state.WildcardProbeHost`) is always allowed,
  so verification and drift checks keep working.
- Routing is unchanged: the list governs certificates only.
- The list belongs to the claim, not the name. Rows cascade when the
  wildcard row is deleted. A claim that passes to someone else reuses the
  row: a reclaim after the 7-day deadline, or a takeover by DNS proof. That
  claim starts with no list and no known hosts, as a new row would. Otherwise
  whoever squatted an unverified wildcard could set a list, and after the
  zone's owner took the claim over, only the squatter's names would get
  certificates. The admission ledger stays, as it does on delete.

## Amendment 3: DNS diagnostics and production ingress (2026-11-03)

The GCP production template enables on-demand TLS with `edge.gregale.dev`.
Its DNS-only A/AAAA records must exist before rollout; operators supply the
reserved public addresses for apex-domain instructions. GCP's access policy
and convergence script retain the Cloudflare origin rules and add two named
public TCP/80,443 rules with a dedicated tag on the control plane alone. The
audit rejects other public origin rules, extra ports or targets, and the tag
on any other instance. Caddy keeps the platform-host Cloudflare boundary.

With a dedicated target configured, an owned platform apex is no longer an
acceptable CNAME shortcut: it may be proxied and cannot complete customer
ACME validation. Address lookup timeouts are inconclusive and never revoke
verification. The doctor stores successful routing and conflicting IPv6
results independently. Its CAA check queries real CAA RRsets through the
system resolvers, follows aliases and parent inheritance, and evaluates
Let's Encrypt authorization for concrete-host certificates. DNS errors remain
pending rather than being interpreted as permission to issue.

## Amendment 4: claim fencing and shared exact-domain admission (2026-11-03)

A wildcard allowlist mutation carries the authorized app and challenge token
into the storage transaction. Under the domain row lock, storage rejects a
mutation if either has changed. Both replace and clear use this fence, so an
in-flight request from a previous claimant cannot change a replacement claim.

Exact domains now use the same durable known-host set and admission ledger as
wildcard leaves. All new hosts share the existing limits in `pkg/api/limits.go`:
40 per registered domain per week, 200 per owner per week, and 150 across the
fleet per three hours. The remaining shared ACME account capacity provides
headroom for renewals and retries. Previously admitted names stay free, including
after a gateway restart. The `wildcard_domain` column retains its legacy name;
for an exact claim it holds the hostname itself.

Apply migrations `20261103232750043` through `20261103232750064` before deploying
the edge changes. The first adds broader checks without validation, the second
validates them without blocking writers, and the third removes the old checks.
A fourth migration, after the DDL transaction commits, seeds known-host rows for
existing verified exact claims without charging the new-host ledger, preserving
their certificate loads during rollout.
The broader checks and runtime admissions remain compatible with the old edge
on rollback.

Repeated permission grants are not evidence of failed issuance: Caddy asks before
loading a valid stored certificate too. The ask-count backoff is removed. The
permission API does not observe ACME outcomes; actual issuance attempts and
failures remain Caddy's responsibility and are diagnosed from its ACME logs.
New-host admission caps remain enforced for both claim types.

When a CNAME lookup returns definite absence, the doctor checks A and AAAA before
classifying routing. Missing routing records revoke verification; a transient
lookup error keeps the result pending. Organization domain inventories include
other members' apps, then apply current app permissions and credential scopes;
creator-owned accounting inventories retain their original scope.

## Amendment 5: claim-bound probes and issuer restrictions (2026-11-03)

The doctor captures the custom-domain claim before querying DNS. Its cached
observation reads, observation writes, certificate-state updates, drift
revocation and certificate-failure email cooldown all require that domain,
app and challenge token still match. A transfer clears the old claim's legacy
doctor observation. An obsolete HTTP doctor request returns `404`; its probe
cannot change the replacement claim or read its cached diagnostics. Tenant
surface observations retain their existing surface anchor.

Certificate-failure notices use the app's current tenant. A shared organization's
active owners and admins receive the notice; a personal app's owner receives it.
The app creator's other organizations and former memberships supply no recipients.

CAA authorization evaluates `validationmethods` against HTTP-01/TLS-ALPN-01 in
on-demand mode and DNS-01 in legacy mode. Separate `issue` records remain
alternatives. An applicable `accounturi` or unknown issuer-specific restriction
keeps permission pending until confirmed against the edge's ACME account;
matching the issuer name alone does not confirm authorization.

## Amendment 6: complete claim checks and notification retries (2026-11-03)

The dashboard binds each cached observation read to the domain, app and token
it listed. On-demand TLS admission carries that same claim identity into
storage, including known-host reads. A replacement claim rejects the old
request. New-host admission locks the claim, then reads its current allowlist
at a fresh snapshot; allowlist writers use the same row lock. Revoked names
remain denied even when previously admitted, while the doctor probe remains
allowed. The legacy wildcard issuer also binds every result write to the
claim it loaded before contacting the CA.

Certificate probes validate the complete DNS answer set, then try its numeric
addresses within one bounded budget. Each remaining address gets a share of
the remaining connection time, so an unavailable first route leaves time for
another address. Connections never resolve the name again, and TLS still
verifies the original hostname and certificate chain.

A failed certificate-alert send releases only its own claim's notification
attempt, identified by the recorded timestamp. Other workers' attempts and
replacement claims retain their cooldowns. Accepted sends retain the existing
24-hour cooldown. Provider idempotency keys hash the claim identity and exact
delivery payload, so retries stay stable across midnight, while transfers and
recipient changes cannot reuse another notification's key.

CAA diagnostics follow the requested certificate: on-demand wildcard claims
still use concrete-host `issue` policy at the probe name; legacy DNS-01 wildcard
certificates query the zone name and use `issuewild` when present, falling back
to `issue` only when it is absent. Remediation names the applicable property.

## Amendment 7 (2026-11-03): retain request authorization and observation order

Per-domain API authorization captures the domain, app and challenge before
route-specific handlers run. Reloads reject a different claim, including moves
between two companies whose apps share a creator. Default selection, retry and
removal repeat the captured claim predicate when writing. Allowlist reads use
the same claim predicate as allowlist writes, so a previous owner cannot read a
replacement owner's hostnames.

Challenge-bound verification publishes its pending certificate state in the
same transaction as verification. A TXT miss only records its check time while
the claim remains unverified; it cannot clear a certificate issued by another
worker. Certificate observations bound to a claim ignore results older than the
last stored check. Internal, unbound repair writes retain their existing behavior.
Memory-store default-domain reads also check that the current domain still
belongs to the selected app, matching PostgreSQL.

Public tenant-hostname reservations check both live wildcard reservations and
foreign verified exact custom domains inside publication, including bulk
onboarding, dry runs, and reconciliation. Exact-domain handoffs within one
account or shared organization remain possible. A tenant's suspension affects a
hostname only after that hostname is verified; an unproved reservation cannot
suppress another tenant's existing custom-domain route or cached route.

DNS drift observations also obey the last stored DNS check time. Superseded
results stop before revoking verification, publishing observations, or emitting
drift events. A superseded synchronous doctor refresh is retryable rather than
reported as a missing domain. Known certificate expiry makes domain readiness
false and a cached doctor report unhealthy, even when issuance status remains
`issued` or renewal has started.

Domain responses expose `verification_expires_at` and `verification_expired`.
An expired, unverified claim has terminal `verification_expired` conditions and
omits its obsolete TXT proof. Resuming `gregale domains setup` for the same app
and environment recreates the claim and prints fresh instructions. If the lease
expires while setup waits, setup stops with the resume command. This preserves
the hard lease: verification and retry do not extend an expired claim.

## Amendment 8 (2026-11-04): preserve observation order and expiry evidence

The verifier carries the drifted claim's DNS-check timestamp into publication,
as well as its app and token. A newer mismatch invalidates that snapshot; an
older successful lookup cannot restore verification after newer DNS drift.
Healthy observations do not advance a drifted claim's mismatch timestamp, and
monitoring an ordinary pending claim does not prevent its initial verification.
Doctor passes are ordered by their start time. Superseded certificate updates
return a stale-observation error before activities, emails or cached observations
are written, and observation upserts independently reject older check times.

A failed TLS probe retains the last known certificate expiry. Expiry metrics
and diagnostics continue reporting that certificate until a successful probe
observes a replacement, or a claim reset explicitly clears it.

Ownership verification uses 16 workers shared by custom domains and tenant
hostnames, interleaving both inventories within a 25-second pass deadline.
Queued work stops at cancellation, allowing the doctor to run even when DNS
servers time out. Unfinished custom-domain checks release only their matching
claim/attempt lease in one bounded cleanup write; cancelled work does not accrue
retry backoff or overwrite a newer worker's lease. These bounds live in
`pkg/api/limits.go`.

## Amendment 9 (2026-11-04): protect legacy observations and cold TLS lookups

A claim-bound certificate update without a DNS-check timestamp is older than
any stored DNS observation. It cannot erase a newer `dns_drifted` transition
or clear its clock. Legacy rows with no DNS observation still accept their
first certificate result; explicit unbound repair operations retain their
existing behavior. This also fences delayed legacy wildcard issuance results.

Before applying its unknown-host lookup bucket, the edge loads durable TLS
admission names in indexed pages into a fixed 2 MiB Bloom filter. Its hash seed
is private to the process. Membership is only a lookup hint: every possible
match still passes the live exact/wildcard, verification, plan, allowlist and
claim-bound admission checks. False positives cost those reads and grant no
permission. Existing certificates therefore survive a process restart or the
recent-host cache's expiry while unknown SNI traffic exhausts the bucket.

The filter refreshes every five minutes and includes local admissions made
during a refresh. Reads share a two-second deadline; a failed refresh retains
the previous filter and retries after one second. Initial failure denies
permission until loading succeeds. The page size and filter bounds live in
`pkg/api/limits.go`; refreshing allocates one replacement filter plus a page,
independently of the number of historical hostnames.

## Amendment 10 (2026-11-04): resume bounded TLS inventory scans

An inventory scan retains its completed pages and cursor across the shared
two-second deadline or a transient page-read failure. The next bounded retry
continues from that cursor rather than starting again. Inventories whose
total read time exceeds one request budget can therefore finish warming up.
An incomplete replacement never grants membership and the previous filter
remains usable until every page of its replacement has loaded. Memory remains
bounded to the current filter, one replacement filter and one page.

## Amendment 11 (2026-11-04): independent doctor claims and legacy recovery

A registered custom domain keeps its own DNS-drift and certificate monitoring
even when a tenant surface reserves the same hostname. Its doctor uses the
live certificate probe and replaces an old surface observation's association.
Surface-only hostnames continue to use cert-engine state. Both doctor inventory
queries exclude deleted surfaces, retaining any independently verified custom
domain at that name so retired hostnames cannot monopolize a bounded batch.

The DNS-01 issuer reads certificates using certmagic's canonical storage keys,
including its `wildcard_` encoding of a wildcard label. A successful CA order
must not be reported as failed because the observer used a different path.

In legacy DNS-01 mode, gatewayd-internal runs a separate bounded recovery pass
at boot and every five minutes. It discovers verified wildcard claims needing
initial issuance, retry, or renewal, including work whose notification was lost.
The issuer atomically claims `custom_domains.cert_retry_at` for the captured app
and challenge before an attempt; eager notifications use the same cooldown.
Replacement claims clear that clock and queued obsolete claims are rejected.
Current billing-owner plan checks apply before contacting the CA.

The pass handles at most 16 claims using four workers, with a two-minute attempt
deadline, four-minute pass deadline, and five-minute durable cooldown. These
bounds live in `pkg/api/limits.go`. Failed or interrupted attempts become due
after the cooldown across process restarts. This recovery is independent of the
tenant-surfaces flag. Caddy on-demand mode skips both discovery and legacy
issuance; Ansible propagates that same mode to the internal gateway. Deploy the
expand migration before the gateway update. Ownership-verification retry APIs
keep their existing unverified-claim semantics.
