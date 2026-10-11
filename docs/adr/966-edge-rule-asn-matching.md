# ADR-966: ASN matching for edge rules

- **Status:** accepted
- **Date:** 2026-10-09
- **Related:** ADR-962 (match expressions), ADR-963 (lists), ADR-143
  (GeoIP database), ADR-091 D21 (geo rules)

## Context

Abuse rarely respects country borders but often comes from a few networks:
cloud and hosting providers for scrapers and credential stuffing, a single
ISP for a targeted attack. Customers can only express that today as long
CIDR lists that drift as providers add ranges. Cloudflare exposes the
client's autonomous system number as a rule field.

## Decision

1. **Data.** gatewayd-internal opens DB-IP's ASN Lite database (CC BY 4.0,
   monthly, same publisher and attribution as the ADR-143 country file) as a
   second `geoip.Reader` at `FAAS_GEOIP_ASN_DB_PATH`
   (`/var/lib/faas/geoip/dbip-asn-lite.mmdb`). The geoip Ansible role stages
   it with the same release name + SHA-256 pinning; with
   `FAAS_GEOIP_AUTO_REFRESH=1` a second watcher refreshes it weekly. A
   missing file never blocks boot.

2. **Field.** Match conditions gain `asn`: the trusted client IP's
   autonomous system, looked up at most once per request and only when a
   condition is evaluated. Values are `13335` or `AS13335`, canonicalized to
   the decimal number. Supported ops: `eq`, `ne`, `in`, `not_in`, `exists`,
   `missing`, `in_list`.

3. **Absent, not guessed.** With no trusted client IP, no database, a lookup
   error or an address outside the dataset, `asn` is absent (ADR-962 §5):
   only `missing` matches. An allow-by-ASN rule therefore fails closed for
   an unknown network and a block-by-ASN rule fails open, the same posture
   as `country`.

4. **Lists.** ADR-963 lists gain the `asn` kind, usable only with the `asn`
   field (migration widens the `kind` CHECK).

5. **Trace.** `gregale edge-rules trace --asn AS13335` simulates the field.

## Consequences

- "Block hosting networks on /login" is one rule plus one `asn` list.
- The gateway holds a second ~10 MB memory-mapped file.
- DB-IP's ASN Lite is coarser than commercial data; misattributed ranges
  are possible, which is why log mode (ADR-960) should precede enforcement.
