# Incidents

When something breaks, the first questions are what is broken, since when,
and why. `gregale alert incident` answers them in one list: your alerts while they
fire, route monitor incidents after a deploy, and platform incidents from the
status page, open ones first.

Incidents are an internal preview (ADR-20261022193512).

## List them

```sh
gregale alert incident
gregale alert incident --app shop --open
gregale alert incident --since 24h
```

```text
STATUS     ACK  SOURCE          APP               DURATION    TITLE                         ID
open       yes  alert           shop              12m         checkout-5xx                  alert-3f0c…
open       -    platform        -                 1h05m       Elevated wake latency         platform-9a1e…
resolved   -    route_monitor   shop              40m         Route requirements violated…  route-77b2…
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
gregale alert incident get INCIDENT_ID
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

## Acknowledge and add notes

```sh
gregale alert incident ack INCIDENT_ID
gregale alert incident note INCIDENT_ID "rolled back to v41; watching error rate"
```

Acknowledging an open incident tells everyone in your account that someone
is on it: the list shows `yes` under `ACK`, and `show` says who and when. An
incident is acknowledged once; acknowledging it again keeps the first. A
resolved incident cannot be acknowledged, but it still takes notes, which is
where follow-ups and links to a postmortem go.

Notes are shared with everyone in your account, oldest first under `Notes`
in `show`, and they cannot be edited or deleted; write a new note to correct
one. A note is at most 2,000 characters, and an incident holds at most 100
notes from your account, its acknowledgement included. Platform incidents
are shared by every account, but your acknowledgement and notes on them are
yours. Acknowledging does not silence an alert: its rule's cool-down and
enabled switch still decide what it sends.

The dashboard shows the same list at **Incidents**, and each incident's page
has an Acknowledge button and a note form.

The API is `GET /v1/incidents`, `GET /v1/incidents/{id}`,
`POST /v1/incidents/{id}/acknowledge` and `POST /v1/incidents/{id}/notes`.
