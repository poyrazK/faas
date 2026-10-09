# ADR-741 · Application change timeline

- **Status:** proposed
- **Date:** 2026-10-09
- **Decision:** Add a read-only `GET /v1/apps/{slug}/changes?since=&until=`
  projection that merges an app's recorded changes and health transitions into
  one newest-first timeline. It adds no table, worker, or write path: every
  event already exists in a store `apid` can read. The capability is
  `internal`, dark behind `FAAS_CHANGE_TIMELINE_ENABLED=1`, until the CLI,
  dashboard, and documentation slices land.
- **Why:** The first question in an incident is "what changed?". Gregale
  already records deploys, traffic shifts, canary steps, rollbacks, edge-rule
  and runtime-config changes, environment changes, route incidents, and — since
  ADR-734 — application health transitions, but each lives behind its own
  endpoint. A customer correlating a 14:05 error spike with a 14:02 deploy has
  to read four surfaces and line up timestamps by hand. External observability
  tools can only answer the question if the customer forwards every one of
  these events; the platform can answer it with no setup.
- **Consequences:**
  - Sources in the first slice, each read with its own bounded query and
    mapped to a closed `source`/`kind` vocabulary:

    | Source | Store | Kinds |
    | --- | --- | --- |
    | `deployment` | `deployment_audit` joined to the app's deployments | the table's closed `deploy.*` set (created, traffic changed, rollout started/completed/aborted, canary step, rolled back, removed, health probe failed/recovered, alert fired, scan regressed) |
    | `edge_rule` | `edge_rule_change_log` | the row's `operation` |
    | `runtime_config` | `app_runtime_config_changes` | `runtime_config.changed` (one upserted row per app, so only the latest change exists, and it carries no detail) |
    | `incident` | `route_monitor_incidents` | `incident.opened`, `incident.closed` |
    | `health` | `app_health_history` (ADR-734) | `health.<recorded kind>` |
    | `activity` | org activity filtered by app | `env.set`, `env.deleted`, `domain.added`, `domain.removed`, `domain.tls_issued`; `deploy.*` activity is skipped because `deployment_audit` already records it |

  - Window defaults to the last 24 hours and is capped at 7 days
    (`ChangeTimelineMaxWindow`). The merged result is capped at
    `ChangeTimelineMaxEvents`, newest first; `truncated: true` reports the cap.
    Each source is read with the same cap so no single source can starve the
    others before the merge.
  - A source that fails is listed in `unavailable_sources` and the response
    stays 200 with the remaining sources. Silently omitting it would read as
    "nothing changed"; failing the whole request would hide the sources that
    did answer.
  - Events carry `at`, `source`, `kind`, optional `deployment_id`, a short
    platform-generated `summary`. Raw `data` payloads, actors, values, and
    error text are never projected. Activity summaries use the org-activity
    resource label (the variable name or domain), which that read model
    already guarantees carries no value or credential.
  - Same authorization as `GET /v1/apps/{slug}/health`: `ScopesReadSurface`
    and app ownership. Available on every plan, like ADR-734's structural
    observations, because every event is the customer's own control-plane
    history rather than metrics.
  - Flag off answers `503 change_timeline_unavailable`.
- **Rejected alternatives:**
  - *A new `app_changes` table written by every producer.* Duplicates six
    existing stores, needs a migration and backfill, and adds a write to every
    change path for a read-only view.
  - *Include `feature_flag_versions`.* Flags are scoped to a project
    environment, not an app; mapping an app to the environments that evaluate
    it is a separate decision. Follow-up.
  - *Include the legacy `audit_log` or the customer events table.* `audit_log`
    has no app key, so filtering would scan JSON across the account without a
    bound; the events table mixes changes with high-volume operational events
    such as every wake.
  - *Overlay metrics in the API response.* Error-rate and latency series
    already have endpoints (`/v1/apps/{slug}/metrics`, request analytics); the
    dashboard joins them client-side. Keeping metrics out keeps this endpoint
    usable on plans without per-app metrics.

## Follow-ups

1. CLI on an existing Observe-group command rather than a new top-level noun
   (API-hosting roadmap guardrail), with `--since` and `--json`.
2. Dashboard panel aligning the timeline with the app's error-rate and p95
   series; `docs/change-timeline.md`; promote to `preview`.
3. Feature-flag versions for apps in a project environment.
