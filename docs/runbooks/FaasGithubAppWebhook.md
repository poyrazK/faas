# FaasGithubAppWebhook

GitHub keeps its own log of every webhook delivery it attempts for the GitHub
App. `githubd` polls that log (`GET /app/hook/deliveries`, App JWT, every five
minutes) because it is the only witness of a delivery that never reached
Gregale: a webhook URL pointing at another host, a webhook secret that differs
from githubd's, or githubd down. githubd's own counters cannot see those.

GitHub does not redeliver failed App deliveries by itself. Each failed delivery
is a push or pull request event Gregale never processed: a missing deployment,
a missing or stale PR preview, or a missed repository rename.

## Symptom

- `FaasGithubAppWebhookFailing`: GitHub's newest delivery got no 2xx response
  and nothing has succeeded since. Push-to-deploy and PR previews are down for
  every customer.
- `FaasGithubAppWebhookDeliveriesLost`: some deliveries in the last hour
  failed although the newest succeeded, typically a githubd restart.
- `FaasGithubAppDeliveryLogUnreadable`: githubd has not read the delivery log
  for an hour, so the two alerts above cannot fire.
- `FaasGithubAppConfigDrift`: the App lacks a webhook event or permission that
  [the GitHub App configuration](../ops/github-app.md) requires. githubd reads
  `GET /app` with each delivery-log poll; the `kind` and `name` labels say
  which. A missing event means GitHub never sends it (a missing `repository`
  event stops rename handling until the hourly installation sync; a missing
  `issues` event stops issue-tracker callbacks). A missing permission makes
  GitHub refuse githubd's writes.

## Recover from FaasGithubAppConfigDrift

1. In the App's settings on GitHub, subscribe the named event under
   **Permissions & events → Subscribe to events**, or grant the named
   permission at the access the configuration page lists.
2. A new permission waits for each installation's owner to accept it. Until
   then those installations keep the old set; organization connects that need
   Members (read) answer `github_permission_pending`.
3. The alert clears on the next poll after `githubd_github_app_config_missing`
   reads 0 for every series.

## Verify

1. Read the cause from `githubd_github_hook_latest_delivery_status_code`:

   | Status | Cause |
   |---|---|
   | 404 | The App's webhook URL is wrong. It must be the platform API host, `https://api.gregale.dev/webhooks/github` in production. The marketing host or a trailing dot returns 404. |
   | 401 | The App's webhook secret differs from githubd's `FAAS_GITHUB_WEBHOOK_SECRET`. |
   | 400 | githubd refused the request. Find the code in the `githubd: webhook delivery rejected` log line. |
   | 0, 502, 503, 504 | githubd or the edge did not answer within GitHub's 10 s timeout. |

2. List the failed deliveries githubd logged, with their GUIDs:

   ```text
   journalctl -u faas-githubd --since -2h | grep 'GitHub could not deliver an App webhook'
   ```

3. Compare with what the listener refused, counted under stable codes:
   `githubd_ops_total{op="webhook_push",code!="ok"}`. `signature_invalid` is
   the secret, `missing_metadata` and `invalid_payload` are a sender problem,
   `enqueue_failed` and `ci_journal_unavailable` are githubd's database.

4. The same rows are in the App's settings: **GitHub App → Advanced → Recent
   Deliveries**.

## Recover

1. Fix the cause. For the URL and events, follow
   [the GitHub App configuration](../ops/github-app.md). For a secret mismatch,
   follow [GithubWebhookSecretRotation](GithubWebhookSecretRotation.md).
2. Redeliver every failed delivery since the outage began: open it in **Recent
   Deliveries** and choose **Redeliver**. Redelivery is safe: githubd
   deduplicates by delivery GUID, and downstream builds are keyed by
   `(delivery_id, app_id)`.
3. Confirm the alert clears. The newest delivery is now a successful
   redelivery, so `githubd_github_hook_latest_delivery_failed` drops to 0 on the
   next poll. Then check `gregalectl github status --status dead` for
   deliveries githubd accepted but could not process
   ([FaasGithubRecoveryQueue](FaasGithubRecoveryQueue.md)).

## Delivery log unreadable

githubd signs the poll with the App private key
(`/run/credentials/faas-githubd.service/`). Look for
`githubd: poll App webhook deliveries` warnings: a 401 means the key does not
belong to the App ID in `FAAS_GITHUB_APP_ID`, and a request error means the
control plane cannot reach `api.github.com`. The poller is wired only when the
App ID and key load; without them the delivery metrics are absent and none of
these alerts fire.
