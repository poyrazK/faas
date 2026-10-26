# Logs

Everything your app writes to standard output and standard error is a log
line. Gregale keeps the lines of every running instance, archives them when
the instance stops, and lets you read, follow, filter and forward them.

```sh
gregale logs my-api                # recent lines
gregale logs my-api --follow       # stream until Ctrl-C
gregale logs my-api --since 1h --level error
```

The dashboard shows the same stream on the app's **Logs** tab.

## Where lines come from

- **Running instances.** Each instance keeps its most recent lines in memory
  (about 10 MiB). `gregale logs` and `--follow` read them directly, so new
  lines show up within a second.
- **Parked and stopped instances.** Gregale scales idle apps to zero. When an
  instance stops, its lines go to the log archive, where they can be read
  within about five minutes. A long-running instance also archives its oldest
  lines once it has written more than its in-memory buffer holds.
- When the app is parked, `gregale logs my-api` shows the archived lines of
  its most recent instance, and `--since 3h` shows every instance that ran in
  those three hours.
- **Build and deploy output** is kept separately: `gregale logs my-api
  --latest-failure` shows the latest failed deployment's build log.

When part of a window can't be shown, for example because an instance wrote
faster than its buffer could hold before the lines were archived, the output
says so instead of skipping silently.

## Retention

| Plan | Archived logs readable for | HTTP request events | Log drains |
|---|---|---|---|
| Free | 1 day | — | — |
| Hobby | 7 days | 3 days | 3 per app, 10 per account |
| Pro | 30 days | 7 days | 10 per app, 30 per account |
| Scale | 90 days | 14 days | 25 per app, 100 per account |

The archive deletes logs 90 days after they are written, whatever your plan,
and deletes all of them when your account is deleted. Lines still in a
running instance's memory are lost if the node
running it fails before they are archived; use a [log drain](#log-drains) when
every line matters.

## Filtering

| Flag | What it shows |
|---|---|
| `--since 15m` or `--since 2026-10-25T08:00:00Z` | Lines from a lookback duration or a time onward |
| `--level info\|warn\|error` | Lines at that level or higher |
| `--grep TEXT` | Lines containing the text (case-insensitive) |
| `--deployment v12` | Lines from one deployment (Hobby and above) |
| `--source http` | HTTP request events instead of runtime lines (Hobby and above) |
| `--status 500`, `--route "GET /orders"` | HTTP events with that status or route |
| `--request ID`, `--trace TRACE_ID` | One request, or every request of a trace |
| `--explain` | A short summary of the latest failure and repeated errors |

`--level` reads the `level` or `severity` field of JSON log lines (the
format Pino, Zap, Logrus and Cloud Logging use) and common text shapes such as
`[ERROR]` or `level=error`. Lines with no recognisable level are left out when
you filter by level.

Save filters you use often with `gregale logs views save NAME ...` and reuse
them with `--view NAME`.

## Reading a whole project

`--project` reads the runtime logs of every service of a
[project](multi-service-apps.md) together, each line prefixed with its
service, as `docker compose logs` does:

```sh
gregale logs --project shop --follow
gregale logs --project shop --service api,billing --since 15m --level error
```

```text
[api] GET /orders 200
[billing] charge ok order=1042
```

`--service` takes service names in either form (`billing_api` or
`billing-api`). With `--follow`, a parked service is asked again every few
seconds, so its lines appear once a call wakes it. Without `--follow`, each
running service prints its recent lines and a parked service is named with
the command that reads its retained logs. `--since`, `--grep` and `--level`
apply to every service; `--json` adds a `service` field to each line. To
follow one request across services, use `--trace` on the app that received it
([ADR-20261026190244](adr/20261026190244-project-logs.md)).

## Reading one archived day

To read one instance's archived lines for one UTC day:

```sh
gregale logs my-api --archive --instance 3e55c98a-5ce8-4a14-a53f-61e645bead56 --date yesterday
```

The full instance id is printed when `gregale logs` shows a parked instance,
and is part of every line in `gregale logs my-api --json`.

## Log drains

A log drain forwards your app's runtime lines to your own logging service as
they are written. Drains are available on Hobby and above.

```sh
LOG_TOKEN='Bearer …' gregale log-drains add --app my-api \
  --url https://logs.example.com/ingest --auth-header-env LOG_TOKEN
gregale log-drains health my-api <drain-id>
```

- `--kind http_json` (the default) posts batches of JSON lines; `--kind otlp`
  sends OTLP/HTTP log records.
- The credential is read from an environment variable, stored encrypted, and
  shown masked.
- The destination must be a public `https://` or `http://` URL; private,
  loopback and link-local addresses are refused, including ones a hostname
  resolves to later.
- `health` shows the queue, delivered and failed counts and the last error.
  Lines a destination keeps refusing are retried and then dead-lettered, so a
  broken drain never holds up your app.

## Limits

- A line is at most 1 MiB. Output without a newline beyond that is dropped.
- Logs have no separate charge.
