# ADR-704: Private Cloudflare DNS configuration inventory

Status: accepted · 2026-10-07

## Context

ADR-703 inventories the supported declared Caddy graph. That graph cannot
identify every configured public hostname or origin. Public DNS answers for
Cloudflare-proxied records expose edge addresses, while the configured origin
can differ. Gregale also has a DNS-only `origin.gregale.dev` path for the timeout
Worker. A filtered A-record lookup or customer domain health check cannot stand
in for complete provider configuration evidence.

## Decision

Add private read-only `edgetopology.CloudflareDNSProbe` for ONE explicitly
reviewed zone ID and canonical name. Freeze those constructor values. Use the
fixed official HTTPS API with ordinary TLS validation and an explicitly supplied
zone-scoped DNS Read API token. No token/environment discovery,
provider writes, environment proxy, redirect following, retries or cached success.
The existing mutable HA and ACME DNS providers remain separate.

Read exact zone details, scan ALL configured record types without hostname,
type, proxy-state or content filters, request shadow metadata without applying
shadow/delegation filters, repeat the entire scan, then re-read zone
details. Require exact reviewed zone identity, active/full/unpaused configuration
and distinct canonical assigned nameservers. Verify every page/count/size/total,
require all full pages and the exact final remainder, reject duplicate record
IDs across pages and changed totals, and return no successful partial inventory.
An empty configured record set can be inventoried but grants no absence proof.

Require bounded unique-key JSON, including opaque nested metadata; reject
case-folded duplicates, trailing values and excessive depth. Keep all limits in
`pkg/api/limits.go`: 1 MiB per response, 16 MiB across collection, 4096 records,
100 records per page, 16 assigned nameservers, 16 JSON levels, 16-byte provider
IDs, 16-byte record type names, 256-byte API token and provider TTL at most 86400.
Reuse canonical DNS label/name bounds and the existing two-second request and
ten-second total topology budgets. TTL 1 means configured Auto, never a cache
expiry or lease for retirement. Reject missing explicit proxy state for address
and CNAME records. Require literal canonical A/AAAA addresses and canonical
CNAME/NS targets; a single final target dot can be normalized for display.
Owners can include a leading wildcard or service/verification underscore labels.
Require every owner to fall within the reviewed zone.

Return zone identity, configured nameservers, zone digest, sorted full record
inventory, versioned configuration digest and completion time. Each record has
its exact ID/name/type, configured TTL/proxy flag and a hash of the FULL raw
record, including unknown settings, timestamps, comments and opaque content.
Expose targets only for A/AAAA/CNAME/NS. Other types, including HTTPS/SVCB/SRV and
future types, remain visible with content represented by their digest. An empty
target never classifies a record as irrelevant or safe to ignore. Never return
API credentials, raw account metadata, TXT values, comments, opaque service
payloads, provider error bodies or arbitrary transport error text.

Require equal raw zone digests and equal complete record-set digests across the
reads. Sort by stable record ID so page ordering alone does not invalidate the
set. Changes to opaque content or metadata invalidate it too. Raw JSON ordering
changes can conservatively invalidate a digest. There is no cross-request
provider transaction, ABA guarantee, future lease or persisted success journal.

## Consequences

This is provider configuration inventory for the reviewed zone, NOT proof of
authoritative served DNS, registrar/parent delegation, DNSSEC, effective wildcard
selection, generated HTTPS records, flattening, external CNAME chains, recursive
cache expiry, Workers, load balancers, Spectrum, custom hostnames, other zones,
raw IP/bypass entrypoints or native Caddy/socket service binding. Configured
proxy state is not effective chain routing. Unsupported records remain
uninterpreted; no safe exclusion, withdrawal clearance or retirement authority
is derived from them. These scopes still require explicit adapters/reconciliation.

No daemon/CLI/API wiring, database/schema changes, automatic collection, public
Apply, PR, push or deployment is added. Execution remains unavailable and private
deployment flags remain off. Local provider fixtures are synthetic contracts;
native DNS/Caddy inventory acceptance, native Linux amd64 KVM `test-metal` and
final `leakcheck` remain pending before enablement. Next work is explicit served
DNS/delegation and native service-scope reconciliation, followed by external
host/socket/network fencing for unreachable startup sessions.

References: [Cloudflare DNS records API](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/list/),
[zone details API](https://developers.cloudflare.com/api/resources/zones/methods/get/),
[proxy status and generated HTTPS records](https://developers.cloudflare.com/dns/proxy-status/).
