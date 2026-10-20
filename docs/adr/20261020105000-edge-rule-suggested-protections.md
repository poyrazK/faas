# ADR-20261020105000: Suggested protections from an app's own traffic

- **Status:** accepted
- **Date:** 2026-10-14
- **Related:** ADR-986 (protection recipes), ADR-1191 (app-wide rules),
  ADR-960 (log mode), ADR-973 (bot signals)

## Context

Protection recipes (ADR-986) make a good rule one click away, but the user
still has to know which recipe their app needs. Request telemetry already
records, per app, the user-agent family of every request (`bot` for clients
that call themselves bots) and per-route request and error counts. That is
enough to say "your sign-in path is being hammered" or "a fifth of your
traffic is bots" without new collection.

## Decision

1. **Suggestions, not automation.** `GET /v1/apps/{slug}/edge-rules/suggestions`
   reads the app's recent request telemetry and returns at most five recipe
   suggestions, each with the parameters to add it with and the observation
   behind it. Nothing is added until the user applies one, and applying goes
   through the ordinary recipe path, so it starts in log mode and every
   validation, plan gate and quota still applies.

2. **Window follows the plan.** Suggestions read the last 7 days, or the
   plan's request-telemetry retention when shorter (3 days on Hobby), and
   each reason quotes the window. Plans without request telemetry (Free) get
   the request-analytics plan gate (402) from the API; the dashboard simply
   shows no suggestions.

3. **Three signals, each with a volume floor.** Thresholds are fixed over
   the window, so a quiet app is not nagged and a shorter window is
   stricter:
   - **Bots:** at least 500 requests from the `bot` user-agent family and at
     least 5% of all requests → `block-unverified-bots`.
   - **Sign-in paths:** a `POST` route whose path has a sign-in, sign-up or
     credential segment (`login`, `signin`, `auth`, `session`, `token`,
     `register`, `password`, `otp`, `verify`, `2fa`, `mfa`, …) with at least
     300 requests, or at least 50 failed requests that are at least 20% of
     its requests → `rate-limit-path` on that path for `POST`. At most two,
     busiest first. Templated routes (`/users/{id}`) are skipped.
   - **Admin:** at least 20 requests to `/admin` or below → `allow-ips-only`
     on `/admin/**`. The allowed IPs are left for the user to enter.
   - **Scanner probes** (added 2026-10-15): at least 20 requests to the
     `block-scanner-paths` prefixes (ADR-986 amendment) →
     `block-scanner-paths`. A prefix that belongs to software an app could
     really run (`/wp-`, `/xmlrpc.php`, `/phpmyadmin`, `/pma/`, `/cgi-bin/`,
     `/server-status`) is left out of the suggested list when the app
     answered most of its requests successfully; secret paths (`/.env`,
     `/.git/`, …) are always kept, since an SPA that answers every path
     with its index page still has no business serving them.

4. **Skip what the app already has.** A recipe already added (recognized by
   its recipe description) is not suggested again, and neither is a path an
   enabled rule of the recipe's kind already matches: a throttle rule for a
   sign-in path, an ip rule for `/admin`. A rule created by a different
   recipe does not count, because its match condition does something else
   (the bot recipe's ip rule covers `/**` but only blocks bots).

5. **Pure and read-only.** The thresholds live in `pkg/api`
   (`SuggestEdgeRuleRecipes`): signals in, ranked suggestions out, tested
   without a database. apid only reads telemetry and rules; no table, no
   background job, no new counter on the request path.

6. **Surfaces.** The edge-rules dashboard page shows a "Suggested for this
   app" section above the recipes, each with an "Add in log mode" button and
   inputs for any required parameter the suggestion cannot fill; a telemetry
   error hides the section rather than failing the page. The CLI's
   `gregale edge-rules suggest` prints each suggestion with the
   `gregale edge-rules recipe` command that adds it.

## Consequences

- An app with real sign-in traffic or bot load sees the matching recipe
  without reading the catalog, and the first step is always log mode.
- Suggestions are only as good as request telemetry: apps with little
  recorded traffic see fewer or none, and the bot signal is the
  self-declared user agent, not the verified-bot check the recipe enforces.
- The path signals (sign-in paths, `/admin`, scanner probes) need per-route
  telemetry, which gateways record only for apps with route metrics turned
  on; other apps report every request under one overflow route and get
  only the bot suggestion. Route labels are also capped at 50 per app on
  each gateway, so on an app with many routes, probes that arrive after the
  cap is reached are not seen.
- Thresholds are fixed constants; tuning them, or adding signals such as
  geographic concentration, is a change to one pure function and its table
  test.
