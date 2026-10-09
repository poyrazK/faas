# Send logs and deployment events to Datadog

If your team already uses Datadog, one command sends an app's runtime logs and
its deployment events there, tagged so they line up with the rest of your
Datadog data. No Datadog Agent runs next to your app: Gregale delivers from its
own control plane, so there is no extra memory to pay for and nothing added to
wake time.

The Datadog integration is an internal preview (ADR-742).

## Connect an app

Create a Datadog API key, then run:

```sh
export DD_API_KEY=...            # from Datadog → Organization Settings → API Keys
gregale add datadog --app my-api --site eu1
```

```text
Datadog (eu1) for my-api
  Logs:   created https://http-intake.logs.datadoghq.eu/api/v2/logs
  Events: created https://api.datadoghq.eu/api/v1/events
```

`--site` is your Datadog site: `us1` (default), `us3`, `us5`, `eu1`, or `ap1`.
The key is read from `DD_API_KEY`, another variable named with
`--api-key-env`, or stdin with `--api-key-stdin`. It is never accepted as a
flag value, so it stays out of your shell history, and Gregale stores it
encrypted and never shows it again.

Running the command again updates the existing setup in place, which is also
how you rotate the key. `--dry-run` shows what would change, and `--remove`
disconnects the app.

## What arrives in Datadog

**Logs.** Every line your app writes to stdout or stderr, as a Datadog log:

| Datadog field | Value |
| --- | --- |
| `message` | the log line |
| `service` | the app slug |
| `status` | `error` for stderr, `info` for stdout |
| `host` | the instance that wrote it |
| `source` | `gregale` |
| tags | `env` (environment), `version` (deployment tag or commit), `deployment_id`, `region` |

`trace_id` and `request_id` are kept as attributes, so a log can be matched to
the request that produced it.

**Events.** Deployments going live or failing, and rollouts completing or
aborting, appear in the Datadog event stream and as overlays on your
dashboards, tagged `service:<app>` and `deployment_id:<id>`. Failed deployments
and aborted rollouts are marked as errors and warnings.

## Delivery

Logs use the same durable queue, retries and health reporting as any other log
drain (`gregale log-drains health`), and events use the same retries and
dead-letter queue as any other webhook. Both only ever send your key to the
Datadog site you chose.

Traces and metrics are not sent yet; they are planned separately.
