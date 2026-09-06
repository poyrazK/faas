# PR preview environments (issue #272 / ADR-095)

Every pull request against a connected GitHub repo gets its own
ephemeral app, deployed on push, routed at a per-PR subdomain,
and torn down on PR close (or after the TTL — whichever comes
first). No CI configuration required; the integration is
triggered by the GitHub App the customer installed via
`gregale connect`.

## URL shape

```
https://pr-{N}.{slug}.apps.<zone>
```

- `{N}` — GitHub PR number, stable across `synchronize` and
  `reopened` events on the same PR.
- `{slug}` — the parent app's slug (the production app the PR
  targets).
- `{zone}` — the platform zone; `apps.gregale.dev` on the
  hosted product.

The wildcard cert at `*.apps.<zone>` covers previews for free —
no per-PR cert provisioning, no DNS work.

## Lifecycle

| Stage | What it means |
|---|---|
| **Open** | A live preview app responding to requests. Wakes on first hit (cold-boot), scales to zero on idle. |
| **Closed** | PR was closed / merged. The preview stays live for **24 hours** so the author (or a reviewer) can revisit the URL one last time. Wakes are still served. |
| **Stale** | 24h grace elapsed. The preview app is queued for teardown on the next janitor tick. Wakes still complete. |
| **Torn down** | App row tombstoned, instances reaped, snapshot freed. The URL returns `410 Gone`. |

A reopened PR during the grace period bumps the row back to
**Open** and the URL starts serving again on the next push.

## Quota

Each preview consumes **one slot** of the customer's
`DeployedAppMax`:

- **Free** — 1 slot total (production + preview). Preview
  attempts beyond the first get `429 deployed_app_capacity`.
- **Hobby** — 5 slots.
- **Pro** — 25 slots.
- **Scale** — 100 slots.

This is the same ceiling production apps use; there is no
separate preview cap. The 7-day default TTL plus the 24h
closed-grace window plus the janitor's per-tick sweep keep
the steady-state preview count bounded — a customer who
opens 20 PRs today will not have 20 previews live a week
from now.

## Fork PRs

PRs from forks are **refused**. The webhook posts a
`gregale-preview` Check Run with `conclusion: neutral` and
`text: "Preview skipped for fork PR (security policy)"`. No
app row is provisioned. This is deliberate: we cannot
guarantee that untrusted code from a fork will not reach the
build VM without a per-install override. The GitHub App needs
`Issues:write` to maintain the single preview status comment on
the PR's issue thread; it does not need `Pull requests:write`.

## Dashboard

Open `/dashboard/apps/{slug}` on the production app — the
**Preview environments** panel lists every preview with its PR
number, current state (chip), the preview URL, and a Copy
URL button (writes to clipboard). A preview row on the apps
list (`/dashboard/apps`) is indented and tagged with a
`preview` chip.

## Pull request feedback

Gregale maintains one bot comment per preview PR. The comment is
updated on `opened`, `synchronize`, `reopened`, and `closed` events,
and includes the preview URL, current lifecycle status, commit SHA,
and one-click destroy link. A hidden marker makes webhook retries and
repeated synchronize events idempotent instead of creating duplicate
comments. The Check Run remains the source of build-stage status.

## Operational notes

- **Stuck teardowns.** If a preview sits in `closed` for
  longer than 24h + one janitor tick, check
  `journalctl -u faas-apid` for `preview janitor: tick
  failed`. The janitor is non-fatal on row errors; a single
  bad row does not abort the sweep.
- **Premature teardown.** A `closed` preview's 24h grace is
  a contract, not a knob. To keep a preview alive beyond
  grace, either reopen the PR (bumps state to `open`,
  resets TTL via the dispatcher) or — for an ad-hoc
  extension — ask support to bump `preview_expires_at`
  manually via SQL.
- **Reusing a torn-down PR number.** GitHub reuses PR
  numbers after a repo transfer or force-push. We treat
  `pr-{N}-{parent-slug}` as stable across events; a
  torn-down PR-{N} that reopens with a fresh head SHA gets
  a fresh `apps` row at the same slug.

## Related

- ADR-095 (decision + schema + state machine rationale).
- `docs/runbooks/PreviewSubdomainRouting.md` — operator
  recovery for routing failures.
- `pkg/githubd` — webhook receiver (PR-A surface).
- `cmd/apid/preview_janitor.go` — the teardown cron.
