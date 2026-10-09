# ADR-742 · One-command Datadog integration

- **Status:** proposed
- **Date:** 2026-10-09
- **Decision:** Add `gregale add datadog --app <slug> --site <site>
  --api-key-secret <NAME>`, which configures two agentless paths from an app
  to the customer's Datadog organization, using building blocks that already
  exist:
  1. **Logs:** a new log-drain kind `datadog` that posts the app's runtime log
     lines to Datadog's HTTP logs intake (`https://http-intake.logs.<site>/api/v2/logs`).
  2. **Change events:** a new app-webhook delivery format `datadog` that posts
     deployment and rollout webhooks to Datadog's Events API
     (`https://api.<site>/api/v1/events`), so deploys and rollbacks appear as
     event overlays on the customer's Datadog dashboards.

  Both carry Datadog's unified service tags: `service` = app slug, `env` =
  the app's environment scope, `version` = deployment tag or commit. The
  capability is `internal`, dark behind `FAAS_DATADOG_INTEGRATION_ENABLED=1`.
- **Why:** Many target customers already run Datadog. Gregale's own
  observability (ADR-740 service map, ADR-741 change timeline) covers what
  only the platform can see; customers who standardise on Datadog still need
  their app's logs and release events there. Today that takes a generic log
  drain whose JSON shape Datadog does not parse as a log message, plus
  hand-built webhook relays, and nothing applies Datadog's tag conventions, so
  logs and events do not correlate with the customer's existing APM.
- **Consequences:**
  - **Sites:** a closed set — `us1` (`datadoghq.com`), `us3`
    (`us3.datadoghq.com`), `us5` (`us5.datadoghq.com`), `eu1`
    (`datadoghq.eu`), `ap1` (`ap1.datadoghq.com`). Intake hosts are derived
    from the site, never accepted as free-form URLs, so the integration cannot
    be pointed at an arbitrary host. US1-FED is excluded until a customer needs
    it.
  - **Key handling:** the customer stores the Datadog API key as an app secret
    and passes its name. Gregale copies the value into existing sealed
    columns, so no migration is needed: `app_log_drains.auth_header_sealed`
    as `DD-API-KEY: <key>`, and `app_webhooks.secret_sealed`, which for the
    `datadog` delivery format is sent as the `DD-API-KEY` header instead of
    being used as an HMAC signing secret (Datadog does not verify Gregale
    signatures). It is never returned, logged, or shown unmasked. Rotating the
    secret and re-running the command updates both.
  - **Log shape:** the `datadog` encoding maps the existing `logdrain.Record`
    to Datadog's attributes: `message` = line, `ddsource` = `gregale`,
    `service`, `hostname` = instance ID, `ddtags` = `env:…,version:…,deployment_id:…`,
    `status` = `error` for stderr and `info` otherwise; trace and request IDs
    stay as attributes for correlation. Delivery reuses the drain's durable
    queue, retries, health endpoint, and degraded alert.
  - **Event shape:** `deployment.live`, `deployment.failed`,
    `rollout.completed`, and `rollout.aborted` map to Datadog events with
    `alert_type` (`success`, `error`, `info`), the same tags, and an
    `aggregation_key` per deployment. Delivery reuses the webhook outbox,
    retries, and dead-lettering. Change-timeline-only sources (health,
    incidents, config) are a follow-up once those have webhook kinds.
  - **Idempotent command:** `gregale add datadog` creates or updates exactly
    one `datadog` drain and one `datadog` webhook per app, and prints what it
    changed; `--dry-run` shows the plan. Removing is `gregale add datadog
    --remove`, which deletes both.
  - **Egress:** delivery runs from Gregale's control plane, not from the app
    VM, so the app needs no egress rule and pays no companion RAM.
- **Rejected alternatives:**
  - *A Datadog Agent companion for logs, traces and metrics.* Companion RAM is
    reserved and billed per instance in addition to the app (docs/companions.md),
    the Agent is large relative to Hobby's 256 MB app limit, and it would start
    on every wake from scale-to-zero. It remains a candidate for traces and
    metrics only (below), opt-in and priced visibly.
  - *Keep the generic `http_json` drain with a header.* Datadog would store
    Gregale's record as attributes with an empty message and no service/env/
    version tags, defeating the point.
  - *Free-form intake URLs.* An integration that sends a customer secret must
    only ever send it to Datadog.
  - *Send events from the change timeline endpoint by polling.* The webhook
    outbox already guarantees delivery and ordering for deployment events.

## Out of scope for this ADR

Traces and metrics. The two candidates are (a) an opt-in `datadog-agent`
companion preset receiving OTLP and DogStatsD inside the instance, with its
RAM shown before deploy, and (b) agentless OTLP export from Gregale to
Datadog's intake. The choice needs a measured comparison of wake latency,
memory, and Datadog intake support, and gets its own ADR.

## Follow-ups

1. Implementation slices: `datadog` log-drain encoding; `datadog` webhook
   format; `gregale add datadog`; docs; promote to `preview`.
2. Health, incident, and config change events once they have webhook kinds.
3. The traces-and-metrics ADR above.
