# Incidents

When something breaks, the first questions are what is broken, since when,
and why. `gregale incidents` answers them in one list: your alerts while they
fire, route monitor incidents after a deploy, and platform incidents from the
status page, open ones first.

Incidents are an internal preview (ADR-20261022193512).

## List them

```sh
gregale incidents
gregale incidents --app shop --open
gregale incidents --since 24h
```

```text
STATUS     SOURCE          APP               DURATION    TITLE                         ID
open       alert           shop              12m         checkout-5xx                  alert-3f0c…
open       platform        -                 1h05m       Elevated wake latency         platform-9a1e…
resolved   route_monitor   shop              40m         Route requirements violated…  route-77b2…
```

The list holds everything still open and whatever resolved in the last 7 days
(`--since` reaches back up to 30 days), at most 100 entries. `--app` keeps one
app's incidents; platform incidents always show, because they can explain an
app's trouble.

| Source | Opens when | Resolves when |
| --- | --- | --- |
| `alert` | an alert rule starts firing | the rule is back to ok |
| `route_monitor` | a production route monitor finds a deploy breaking route requirements | the routes recover or a newer deploy replaces it |
| `platform` | Gregale posts an incident on the status page | the status page marks it resolved |

An alert that keeps breaching stays one incident, however many notifications
its cool-down lets through. If a rule cannot read its metric for a while, the
incident stays open until the rule can tell again.

## Look at one

```sh
gregale incidents show INCIDENT_ID
```

```text
checkout-5xx  (alert, resolved)
  app       shop
  opened    2026-10-22 11:40:00 UTC
  resolved  2026-10-22 11:55:00 UTC  (after 15m)
  metric    error_rate_pct

Notifications
  11:50:00  observed 9.5  delivered
  11:40:00  observed 7.2  delivered

What the logs showed
  42 errors, 3 warnings, 40 HTTP 5xx (most errors on /checkout: 38)
  search: /dashboard/apps/shop/logs?level=error&since=…

What changed
  2026-10-22 11:35  Deployment created
```

`show` adds what the platform has for the incident: the notifications an
alert sent and what the logs showed when it last fired (with
[log search](log-search.md) on), a platform incident's status updates, and for an
app incident the app's changes from an hour before it opened until it
resolved. Evidence that could not be read is named under `Could not read`
rather than left out silently.

The API is `GET /v1/incidents` and `GET /v1/incidents/{id}`.
