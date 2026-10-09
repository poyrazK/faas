# What changed: the change timeline

When an app's errors or latency move, the first question is what changed.
The change timeline puts everything Gregale already records about an app on
one newest-first list: deploys and rollouts, configuration changes, route
incidents, and health transitions. Nothing needs to be instrumented or
forwarded.

The change timeline is an internal preview (ADR-741). It is available on every
plan once enabled for your deployment.

## Read the timeline

```sh
gregale app my-api changes                       # last 24 hours
gregale app my-api changes --since 2026-10-08T12:00:00Z
gregale app my-api changes --json                # for scripts and agents
```

```text
my-api: changes from 2026-10-08T14:00:00Z to 2026-10-09T14:00:00Z
2026-10-09 14:07:00Z  health         Health changed from healthy to degraded
2026-10-09 14:05:10Z  incident       Route incident opened
2026-10-09 14:02:00Z  deployment     Rollout of deployment a1b2c3d4 started
2026-10-09 13:58:31Z  activity       Environment variable PAYMENT_TIMEOUT set
```

On the dashboard, the **What changed** page is at
`/dashboard/apps/<app>/changes`. Changes appear as markers under the app's
error-rate and p95 latency charts for the last 24 hours or 7 days, so a spike
and the change just before it line up. Hover a marker for its details.

The API returns the same list:

```sh
curl -H "Authorization: Bearer $GREGALE_TOKEN" \
  "https://api.gregale.dev/v1/apps/my-api/changes?since=2026-10-08T12:00:00Z"
```

`since` and `until` are RFC 3339 times. The window defaults to the last 24
hours, `until` cannot be in the future, and the window can be at most 7 days.

## What appears

| Source | Changes |
| --- | --- |
| `deployment` | Deployments created, traffic changes, rollouts started, completed or aborted, canary steps, rollbacks, failed and recovered health probes, fired alerts, scan regressions |
| `edge_rule` | Edge rules created, updated or deleted |
| `runtime_config` | The most recent runtime configuration change (older ones are not kept) |
| `incident` | Route incidents opened and closed |
| `health` | Recorded app health transitions, for example healthy to degraded |
| `activity` | Environment variables set or deleted; domains added, removed, or issued a TLS certificate |

Summaries name what changed, such as a variable name or domain. They never
contain values, secrets, or who made the change.

## Limits

- At most 200 changes are returned, newest first. When more exist in the
  window, `truncated` is `true`; narrow the window to see older ones.
- If one kind of change cannot be read, it is listed in `unavailable_sources`
  and the others are still returned. The CLI and dashboard show a warning so a
  missing source is never mistaken for "nothing changed".
- Feature flag changes are not included yet.
