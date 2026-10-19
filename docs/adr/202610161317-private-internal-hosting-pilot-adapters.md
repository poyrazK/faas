# ADR-202610161317 · Private internal hosting pilot adapters

- **Status:** accepted for a default-off disposable pilot; live qualification pending.
- **Date:** 2026-10-16
- **Amends:** [custom domains](202610160959-internal-hosting-custom-domains.md), [standard-plan recovery](202610161106-standard-plan-hosting-domain-recovery.md), and [traffic observations](202610161159-internal-hosting-traffic-observations.md).

## Decision

Bundle the qualification runner's operator-controller contracts with a private
local pilot workflow. Preparation writes new local files only. An explicit
qualify command uses already applied Terraform and an existing isolated apid,
project, account and environment. It never provisions infrastructure, enables
product admission/serving, creates a subscription, or deploys a Worker.

A Cloudflare DNS adapter uses a separate customer-DNS account and zone, scoped
DNS credentials, and a read token for the hosting zone/Access inventory. It
journals record content and a unique run comment before every create. A lost
response can recover only an exact matching owned record; an absent uncertain
create fails for operator review. Idempotent cleanup removes only journaled
records after the runner verifies provider/catalog deletion and terminal R2
heads. Unrelated or changed records are preserved. The adapter does not mint
Access JWTs, weaken policies or launch interactive login; it reads protected
operator-supplied JWT files. Claims are a local sanity check; the edge still
verifies signatures and actual Access authorization.

`FAAS_FRONTEND_HOSTING_PILOT_ENABLED` is an independent, default-off bootstrap
opt-in. Together with `FAAS_FRONTEND_HOSTING_PILOT_RELAY_SOCKET`, it replaces only
the hosting custom-domain and analytics clients' HTTP transports. The socket and
its directory must be private, symlink-free and owned by apid's effective uid;
checks repeat on requests and dialing. The local hop accepts only the fixed
Cloudflare management API and has no TCP or direct-provider fallback. The relay
forwards through normal certificate-verified HTTPS, preserving proxy and CA
configuration. No global TLS interception or added trust root is introduced.
R2, KV, serving Workers, other apid providers and normal default transports are
unchanged. A missing, unsafe or stopped relay fails these pilot management calls.

The relay accepts only the configured hosting zone/account, disposable hostname,
owned object IDs and the pinned site's analytics dataset/origin. Its journal
contains counters and resource ownership, never authorization headers, JWTs,
request bodies or analytics SQL. DNS record values remain in the protected DNS
ownership journal. Scoped provider faults can lose a successful create response,
isolate domain-management calls, or isolate analytics queries. A watchdog in the
separate, long-running relay restores expired faults independently of qualification
and short-lived controllers. This is a disposable pilot process, not a new
production hosting daemon or a customer serving dependency.

## Recovery and boundaries

Each fault is journaled before it is armed, scoped to one run/site/claim, and
restored idempotently. Inspection independently counts provider hostname objects.
All writers for the isolated pilot must use the relay; unrelated manual/provider
writers invalidate attempt-count evidence. Relay death stops pilot management;
it does not silently bypass fault injection. Restore/restart it before cleanup.
A stopped process cannot acknowledge restoration, so the runner preserves DNS
and reports blocked cleanup. Journals are retained after successful cleanup.

The standard-plan ambiguous-create fence remains unchanged. The controller never
returns a candidate provider ID for automatic adoption. A new run-bound operator
review is still required for exactly one fenced recovery request. The recovery
cleanup command restores journaled faults, rechecks account/origin/Worker/site
identity and authenticated artifact inventory, and verifies canonical/domain
terminal heads before clearing owned DNS. It records cleanup separately and never
turns a failed qualification into a successful one.

Existing one-hour Access applications require refresh during longer live runs.
A protected canonical JWT file can be reread on edge probes; custom-domain JWTs
are refreshed through the token controller once per minute. Operators must obtain
and renew the real identities through their approved sign-in flow. No longer
session, service-policy bypass, live credentials or live provider evidence is
inferred from portable tests.
