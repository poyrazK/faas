# FaasGithubdPathFilterDegraded

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`,
the `FaasGithubdPathFilterDegraded` + `…Warn` alert blocks
(issue #432 phase 5 follow-up, ADR-050 §109).

Metrics: `githubd_path_filter_total{mode}` — emitted once per
inbound push webhook by `pkg/githubd/service.go:lookupChangedFiles`
after the mode is decided. `mode` ∈ {paths, full_fallback,
truncated, error, breaker_open}. Pre-flip the default was
`full_fallback` so a credentials-missing box was indistinguishable
from a healthy one; post-flip `paths` is the default posture and
`error` / `breaker_open` mean the dispatcher tried the compare
API and failed (or tripped the breaker after 3 consecutive
failures).

Severity: `warn` after 5m and again after 10m. ADR-1236 demoted the
10m page: deliveries are durable and wait for business hours.

## Symptom

The alert fires when the rate of `error | breaker_open` pushes
stays above zero for the threshold window:

| Alert | Trigger | For | Severity |
|---|---|---|---|
| `FaasGithubdPathFilterDegradedWarn` | `sum(rate(...{mode=~"error\|breaker_open"}[5m])) > 0` | 5m | warn |
| `FaasGithubdPathFilterDegraded` | `sum(rate(...{mode=~"error\|breaker_open"}[5m])) > 0` | 10m | page |

`mode=error` fires when:
- The GitHub App credentials aren't provisioned (FAAS_GITHUB_APP_ID
  unset, FAAS_GITHUB_APP_KEY_PATH unreadable, or
  `githubd.NewAppAuth` fails). `cmd/githubd/main.go` wires the
  `NewUnavailableChangedFiles` stub in this case — the stub
  returns `ErrUnavailable` on every call.
- The GitHub compare API returns a transport error, 4xx, or 5xx.
- The URL couldn't be parsed (owner/repo/full-name malformed).

`mode=breaker_open` fires after 3 consecutive `error` pushes
within the 5-minute failure window — the breaker in
`pkg/githubd/changedfiles.go` trips for 10 minutes. Subsequent
pushes short-circuit at the breaker and never hit the API.

The `NewUnavailableChangedFiles` stub wired on credentials-missing
boxes is wrapped in `NewBreakerChangedFiles` (same as the
production path) — so even on a box that will *never* recover
until someone provisions the App credentials, the metric ticks
the natural progression `error` → `breaker_open` and the breaker
cools down after 10 minutes. This bounds metric-series cardinality
on long-lived misconfiguration and gives operators a single
stable signal after the first 3 pushes.

`mode=full_fallback` is **not** in this alert's expression. It
fires legitimately on first-push-on-a-branch events where
`ev.Before` is empty and the dispatcher cannot form a compare
URL — that's not a degradation. A noise floor of 1-2% of all
pushes is expected on healthy boxes.

`mode=truncated` is also not in this alert. A push whose diff
exceeds GitHub's compare-API caps (300 files / 250 commits) lands
in truncated mode and rebuilds everything by design (ADR-050
§103-109). Brief truncated spikes are normal on big merge
events.

## Verify

```bash
# 1. Confirm the mode breakdown on the box that's paging.
curl -s http://localhost:9100/metrics | grep githubd_path_filter_total

# 2. Check the githubd boot log for the credential-provisioning
#    warning that explains mode=error.
journalctl -u faas-githubd --since "10m ago" | grep -E "ChangedFiles|GitHub App credentials"

# 3. Inspect the breaker state (if mode=breaker_open).
journalctl -u faas-githubd --since "10m ago" | grep -E "circuit|breaker|tripped"

# 4. Verify outbound reachability to api.github.com.
curl -sS --max-time 5 https://api.github.com/meta | jq . | head -20

# 5. If credentials look OK but the breaker is open, the upstream
#    is rate-limiting or 5xx-ing; check the GitHub status page
#    (https://www.githubstatus.com) before paging further.
```

## Recover

The fix depends on which sub-case is firing:

| Sub-case | Fix |
|---|---|
| FAAS_GITHUB_APP_ID unset | Set the env var on the box + restart `faas-githubd.service`. The boot log should now show `OAuth + Checks wired`. |
| FAAS_GITHUB_APP_KEY_PATH unreadable | Verify the key file exists, is mode 0400, owned by `root:root` (per spec §11 line 398). `LoadCredential` from `pkg/secretbox` enforces 0o400 strict equality — fix the perms and restart. |
| GitHub App installation revoked | Re-install the App on the customer's repo (or ask the customer to). |
| api.github.com unreachable | Network egress is blocked. Check `deploy/nftables/` for the githubd egress allowlist; ensure `api.github.com` is permitted. |
| Rate limit (compare API 5000/h per install) | The breaker cools down for 10 minutes automatically; subsequent pushes return `breaker_open` until the cooldown elapses. If a single install is hot, consider whether the install-token cache TTL should be tuned. |

Once the next push ticks `mode=paths` (visible in `/metrics`), the
alert auto-clears. The `for: 10m` window means you have a few
minutes to act before the page lands — start at step 1 and 2 in
parallel.

## Silence

If the alert is firing due to a known GitHub outage (status
`githubstatus.com` shows degraded), silence for 1 h via the
`alertmanager` UI; the upstream will recover and the breaker
will reset on its own. Avoid silencing without checking the
status page — mode=error + breaker_open on a healthy GitHub
usually means the credentials on this box are stale.

## Related

- ADR-050 §103-109 — repo decomposition + path-filter posture
- ADR-012 — githubd daemon
- `pkg/githubd/service.go:lookupChangedFiles` — the mode decision
- `pkg/githubd/changedfiles.go:NewUnavailableChangedFiles` — the
  credentials-missing stub
- `pkg/wire/metrics.go:ObserveGithubdPathFilter` — the metric emit
