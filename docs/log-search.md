# Log search

`gregale logs` is the fastest way to watch what an app is doing now. Log
search answers the other question: what happened, where, and how often.
It searches the app's log ledger (HTTP requests today; runtime warnings and
errors, builds and deploys as they are added) across your plan's log
retention, filters by text, level, route, status, release, request and
trace, and counts the matches.

Log search is an internal preview (ADR-20261018083536). It is available on
every plan once enabled for your deployment.

## Search

```sh
gregale logs search my-api --level error --since 6h
gregale logs search my-api --status 5xx --route /checkout --since 2h
gregale logs search my-api --q timeout --since 1h --json
gregale logs search my-api --trace 4bf92f3577b34da6a3ce929d0e0e4736
```

Results are newest first, up to 100 per page (`--limit`, at most 200). When
more events match, the output ends with a `--cursor` to pass for the next page.

Filters combine: every event shown matches all of them.

| Flag | Matches |
|---|---|
| `--q TEXT` | the message contains TEXT, ignoring case (2–256 characters) |
| `--level error,warn` | any listed level |
| `--status 502` or `--status 5xx` | one status or a whole class |
| `--route ROUTE` | one route template exactly, such as `/checkout/{id}` |
| `--source SOURCE` | `runtime`, `build`, `deploy`, `http`, `network` or `dns` |
| `--deployment ID` | events from one deployment |
| `--request ID`, `--trace ID` | one request or one distributed trace |
| `--since`, `--until` | the window: `15m`, `6h`, `3d` or RFC 3339; default the last hour |

## Count

```sh
gregale logs facets my-api --since 24h
gregale logs facets my-api --source http --status 5xx --since 7d
```

Facets take the same filters and count the matches by source, level, status
class and the ten busiest routes:

```text
my-api: 1284 events from 2026-10-17T12:00:00Z to 2026-10-18T12:00:00Z
  Sources: http 1203, runtime 81
  Levels:  error 64, warn 17
  Status:  2xx 1150, 4xx 31, 5xx 22
  Routes:  /checkout 611, /cart 402, /health 190
```

## Limits

- Searches reach back as far as your plan keeps logs (1, 7, 30 or 90 days).
  An older `--since` starts at the retention boundary instead.
- A text search (`--q`) covers at most 24 hours; facets cover at most 7 days.
  A longer window is rejected with `log_search_window_too_large`.
- Each search has 5 seconds to finish. If it runs out, you get
  `log_search_timeout`: narrow the window or add a filter.

The API is `GET /v1/apps/{slug}/log-events` and
`GET /v1/apps/{slug}/log-events/facets`; see the API reference.
