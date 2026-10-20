# ADR-986: Edge protection recipes

- **Status:** accepted
- **Date:** 2026-10-13
- **Related:** ADR-960 (log mode and hit counts), ADR-962 (match
  expressions), ADR-964 (security events), ADR-973 (bot signals),
  `docs/api-hosting-roadmap.md` ("What not to do next" §1)

## Context

Edge rules gained conditions, lists, bot signals, composite throttles and a
log mode, but only through the API, CLI and SDKs. The dashboard's edge-rules
page can list, toggle, switch log/enforce and delete rules, trace a
request and show security events; it creates exactly one preset (security
headers) and a raw JSON form. A customer who wants "block fake bots" or
"rate-limit my login" has to learn the rule model first. The roadmap also
asks not to add more isolated edge-rule kinds before the core journeys are
coherent.

## Decision

1. **A recipe is a template, not a feature.** `pkg/api` holds a small
   catalog; each recipe has an id, a description and a few parameters, and
   expands to one ordinary `CreateEdgeRuleRequest`. The dashboard and the CLI
   both expand it and send it through the normal create path, so
   validation, plan gates, quotas and the rule-set history are unchanged.
   No new rule kind, API endpoint, column or gateway code.

2. **Log mode first.** A recipe's rule is created in log mode unless the
   caller asks to enforce. The existing rules table shows its matches and
   the security-events section shows sample requests; one click starts
   enforcing.

3. **Blocking recipes count exactly what they would block.** They use
   `kind=ip` denying every client, paired with a match condition, so the
   condition alone decides what is blocked and a log-mode match count is a
   would-block count. (`kind=respond` is preview-only; `ip`/`geo` allow and
   deny lists would count every request on the path in log mode.) This makes
   them available on plans with IP rules (Hobby and above); the page says so
   on Free. Like any `ip` rule, an untrusted client IP is refused.

4. **Initial catalog.**
   - `block-unverified-bots`: `ua_family = bot` and no `verified_bot`.
   - `rate-limit-path`: `kind=throttle` keyed by client IP (default
     `POST /login`, 10/min). In log mode a throttle counts every request the
     limit covers, not only would-be rejections; the recipe says so.
   - `allow-ips-only`: block a path (default `/admin/**`) unless the client
     IP is in a list; an unknown client IP is blocked.
   - `block-countries`: block clients located in listed countries; an
     unknown country is let through.

5. **Provenance.** The rule is named after the recipe and its description
   records the recipe id; the page marks recipes that already have a rule.
   Rules stay ordinary afterwards — editing or deleting them is the normal
   rule workflow.

## Consequences

- The common protections are one click (or one CLI command,
  `gregale edge-rules recipe <id>`) and start safe.
- Adding a recipe is a reviewed code change to the catalog.
- Recipes cover every hostname the app serves, custom domains included
  (`match_host: "@app"`, ADR-1191); `--host` restricts one to a single host.

## Amendment (2026-10-14): "alert me"

A recipe can also create a spike alert for the rule it adds: an
`edge_rule_activity` alert (ADR-1192 amendment) scoped to that rule, over a
1-hour window, firing above a threshold (default 100 matches per hour), to
a webhook URL and secret the user supplies — alerts deliver to webhooks
only. Counting both modes means the alert keeps working after "Start
enforcing". The dashboard checks the alert inputs and the plan's alert
allowance before creating the rule; if the alert still fails (for example
the webhook URL is refused), the rule stays and the page says the alert was
not added. CLI: `gregale edge-rules recipe <id> --alert-webhook-url …
(--alert-webhook-secret-stdin | --alert-webhook-secret …) [--alert-threshold N]`.

## Amendment (2026-10-15): block scanner paths

A fifth recipe, `block-scanner-paths`, answers 403 to requests whose path
starts with one of the prefixes vulnerability scanners probe on every
public site: `/.env`, `/.git/`, `/.svn/`, `/.hg/`, `/.aws/`, `/.ssh/`,
`/.DS_Store`, `/vendor/phpunit/`, `/wp-`, `/xmlrpc.php`, `/phpmyadmin`,
`/pma/`, `/cgi-bin/` and `/server-status`. Like the other blocking recipes
it is one `kind=ip` deny-all rule on `/**` whose match condition (`path`
`prefix` over the list) decides what is blocked, so its log-mode count is
the requests it would block. The list is the recipe's `paths` parameter:
the user can drop a prefix their app really serves (a WordPress site's
`/wp-admin`) or add their own, up to the 64-value condition limit.
