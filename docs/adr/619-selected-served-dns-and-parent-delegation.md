# ADR-619: Selected served DNS and parent delegation observations

Status: accepted · 2026-10-07

## Context

ADR-618 inventories Cloudflare configuration but does not query served DNS or
parent delegation. Recursive answers cannot identify configured origins behind
proxied records. A DNS-only origin can also disagree with provider configuration
or remain reachable through a different delegation. Native ingress reconciliation
needs bounded observations of those explicit paths before connecting them to
host/listener/service identities.

## Decision

Add private read-only `CloudflareDNSProbe.ObserveServed`. Freeze an explicit review
before network access: exact provider configuration digest, strict ancestor
parent zone, reviewed parent and child nameserver names and ALL of their declared
literal TCP endpoints, and selected concrete in-zone A/AAAA/CNAME questions.
Require distinct canonical unicast IP/port endpoints, bounded distinct authority
names and questions, and a precomputed bound for both rounds. No endpoint or
authority can disappear because it fails a probe. Do not discover addresses from
DNS, delegate to the system resolver, follow referrals/aliases or scan hosts.

Collect fresh ADR-618 provider inventory before DNS and again after both DNS
rounds. Require the exact reviewed digest and the complete configured child
nameserver name set. Derive expected selected RRsets from every matching exact
configured owner/type record; reject missing/opaque targets, missing proxy state
or any proxied record at that owner. Other question types and wildcard owners
are unsupported. Proxied anycast answers cannot attest configured origin IPs.
An exact CNAME observation proves that alias RRset only; external targets and
effective flattening/chain routing remain unresolved.

Each DNS exchange opens a fresh TCP connection to its reviewed literal endpoint,
sends one IN query with a fresh random ID and RD=false, then closes it. No UDP,
EDNS/DO, automatic retries, environment proxy, resolver cache or pooling. Bound
the two-byte length-prefixed response before allocation. Verify exact question
count, ID, name/type/class, QR/query opcode, NOERROR, RD=false, reserved Z=false
and TC=false. Walk every declared RR in the frame before using the shared DNS
decoder; reject dishonest counts, compression errors and trailing bytes. Never
include arbitrary server/transport error content in errors or logs.

At EVERY parent endpoint require authoritative parent-apex SOA and complete NS
answers, then a nonauthoritative empty-answer referral with the exact child's NS
set in the authority section. This detects an intervening cut or wrong referral;
it does not trace/validate the chain back to the root. At EVERY child endpoint
require authoritative child-apex SOA/NS and exact complete positive selected
RRsets. Refuse cached/non-authoritative, negative, empty, extra, aliased/mixed,
wrong-owner/class or duplicate records. Optional authority sections must contain
the exact reviewed zone NS set. Optional additional records must be A/AAAA glue
for reviewed nameservers and reviewed IPs; never dial glue. Uninterpreted DS,
DNSSEC, OPT or other response sections remain unsupported, not safe exclusions.

Repeat all parent, child and selected queries. Require stable NS/SOA/selected
values and glue sets at every endpoint across the two rounds, plus identical
provider inventory digests. SOA comparison includes all fields, including the
serial, with no ordering or wall-clock meaning assigned to it. DNS names are
case-insensitive; record order is irrelevant. TTL can vary and is retained only
as observed minimum TTL, never as a cache-expiry or retirement lease.

Return a separate observation type with the fresh provider inventory, frozen
review, both rounds' explicit endpoint/question/value/glue witnesses, raw response
digests and completion time. Any late failure returns zero result. All limits
live in `pkg/api/limits.go`: 32 selected questions, 32 total endpoints, 256 DNS
exchanges, 16 KiB per DNS frame, 128 RRs per frame and 30 seconds total. Reuse
the 16-name authority bound and two-second cancellable per-request budget.

## Consequences

This proves only observed agreement for the selected exact DNS-only RRsets and
the reviewed parent/child endpoints. Plaintext DNS/AA flags do not authenticate
endpoint ownership. Reviewed name-to-IP mappings and the parent starting point
remain trust assumptions; DNSSEC, registrar/root-chain validation, other anycast
sites/addresses, wildcard expansion, all other zone records, recursive caches,
effective CNAME/flattening/CDN/Worker/load-balancer routing, other zones, raw IP
bypasses, native sockets and Caddy/service identities remain outside this scope.
Equal rounds/bookends do not exclude ABA changes or grant future traffic leases,
withdrawal clearance, VM quiescence or predecessor retirement authority.

No daemon/CLI/API wiring, schema/database publication, DNS writes, public Apply,
PR, push or deployment is added. Execution remains unavailable and private flags
remain off. Local TCP/provider fixtures are synthetic contracts. Native DNS/Caddy
acceptance, native Linux amd64 KVM `test-metal` and final `leakcheck` remain pending
before enablement. Next work is native origin/listener/service-scope reconciliation,
then external host/socket/network fencing for unreachable startup sessions.

References: [DNS message and RR format (RFC 1035)](https://www.rfc-editor.org/rfc/rfc1035.html),
[referrals and wildcard/alias semantics (RFC 1034)](https://www.rfc-editor.org/rfc/rfc1034.html),
[DNS over TCP (RFC 7766)](https://www.rfc-editor.org/rfc/rfc7766.html).
