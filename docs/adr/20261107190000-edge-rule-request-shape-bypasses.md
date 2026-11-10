# ADR-20261107190000: Edge rules read requests the way apps do

Date: 2026-11-07
Status: Accepted
Amends: [ADR-091](091-edge-rules.md) (edge rules), [ADR-962](962-edge-rule-match-expressions.md)
(match expressions)

## Context

An edge-rules review on 2026-11-07 found three places where a gate or a
validate rule read a request differently from the app behind it, so a
request the rule meant to stop reached the app unchecked.

1. **Conditions on gates read the raw path and method.** ADR-091
   (amendments 2026-10-28 and 2026-11-02) made the `match_path` of the kinds
   that deny or constrain (`jwt`, `ip`, `geo`, `limit`, `throttle`,
   `validate`, `maintenance`, `waf`, `challenge`) also match the path's
   normalized forms: dot segments and duplicate slashes resolved, `;`
   parameters dropped, case folded. They also made a `GET` selector cover
   `HEAD`. A `match` condition (ADR-962) still compared the raw path and
   method. "JWT on `/**` except `path prefix /public`" let
   `/public/../admin` through, and "`path prefix /admin`" missed `//admin`
   and `/ADMIN`; frameworks route all of them as `/admin`.
2. **Validate bodies were decoded with exact keys.** The schema check
   decodes a body into a map: keys are case-sensitive and the last duplicate
   wins. Go's `encoding/json` struct binding and ASP.NET Core's web defaults
   bind property names case-insensitively, so `{"role":"user","ROLE":"admin"}`
   passed a schema allowing only `role: user` and reached a Go app as
   `role=admin`.
3. **Validate query parameters dropped `;` pairs.** The gateway read the
   query with Go's `url.Query`, which silently drops a pair that contains
   `;`, but it forwards the raw query unchanged. `?id=1%20OR%201=1;--`
   showed the rule no `id` at all while the app (Express, Python, Rack, PHP)
   read `id=1 OR 1=1;--`, so an optional `integer` or `pattern` check never
   ran. `query:<name>` conditions and `${query:name}` templates had the same
   gap.

## Decision

1. A gate kind's condition holds when it holds for **any** path form its
   `match_path` covers (`api.ProtectiveEdgeRulePaths`), and for `HEAD` also
   when it holds for `GET` (`EdgeRuleMatchProgram.MatchesProtective`). Every
   extra form only adds protection, as for the selectors. Other kinds keep
   exact matching. The gateway and the trace simulator share the helper.
2. A `kind=validate` body that names a property twice in one object, at any
   depth, exactly or in spellings that differ only in case, fails the rule
   with `expected: unique_properties` before the schema runs. This is
   independent of `reject_on_unknown_fields`. A body that legitimately uses
   case-distinct keys fails; such APIs are rare and the alternative is a
   bypass for every Go or .NET app.
3. Edge rules parse the query as apps do (`api.ParseEdgeRuleQuery`): pairs
   split on `&` only, `;` kept in the value. A validate rule refuses a query
   whose percent-escapes do not decode (`expected: query_string`);
   conditions and templates keep such a pair as written.

## Consequences

- Gates written with conditions now cover the request forms their
  `match_path` already covered; a few requests that slipped through before
  are refused or challenged.
- Validate rules refuse duplicate and case-variant keys and undecodable
  queries with 422 in `block` mode (recorded in `observe` and `warn`).
- `match_headers` exact-value selectors are unchanged; gates should not rely
  on a header value's spelling.
