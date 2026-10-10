# ADR-967: Dynamic values in header and redirect actions

- **Status:** accepted
- **Date:** 2026-10-10
- **Related:** ADR-962 (match expressions), ADR-966 (ASN), ADR-091 (edge rules)

## Context

Header and redirect actions only take literal values. Common needs — pass
the visitor's country or ASN to the app, tag requests with a tenant header,
redirect a moved site while keeping the path and query — need request
values. Cloudflare's transform and redirect rules offer dynamic expressions.

## Decision

1. **Opt-in templates.** A header op (`add`/`set`) and a redirect action
   gain `template: true`. Only then are `${name}` references expanded, so an
   existing value that happens to contain `${` keeps its literal meaning.
   `$$` is a literal `$`; a bare `$` or an unknown name is a validation error.

2. **Values.** `host`, `path`, `method`, `query` (raw query string),
   `client_ip`, `country`, `asn`, `request_id`, `header:<name>`,
   `query:<name>`, `cookie:<name>`. They come from the same request snapshot
   match conditions use (ADR-962): the trusted client IP, and country/ASN
   looked up lazily and only when referenced. A value the request does not
   carry expands to empty.

3. **Header safety.** Control characters (including CR/LF) are dropped, so a
   request value cannot inject headers, and each value is capped at 1 KiB.

4. **Redirect safety.** Values are URL-escaped (`path` and `query` keep their
   already-escaped form). The target must start with a literal `/` or
   `http(s)://`, only `${host}` may appear before the path, and a leading
   `//` in the result is collapsed — a request cannot pick the redirect's
   host or make it protocol-relative.

5. **Bounds and compilation.** At most 2 KiB and 16 values per template.
   Templates are validated by apid, compiled once per host load by the
   gateway (a stored template that no longer compiles drops its rule, like
   an unparseable `match_path`), and rendered by the trace simulator with
   the simulated request.

## Consequences

- "Add `X-Client-Country: ${country}`" and "redirect old.example to
  `https://new.example${path}?${query}`" are single rules.
- Templated values are per-request work on matched rules only; untemplated
  rules take the existing path unchanged.
