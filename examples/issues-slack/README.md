# Gregale Issues → Slack

A receiver for existing Gregale outgoing webhooks that posts Issues alerts to a
Slack channel. The Node SDK verifies signatures and formats messages; a
customer-owned PostgreSQL database stores delivery receipts across restarts and
multiple receiver processes. No live Slack workspace is connected by this example.

## Run

From the repository root:

```sh
npm --prefix sdk/node ci --ignore-scripts
npm --prefix sdk/node run build
npm --prefix examples/issues-slack ci --ignore-scripts
```

Configure these environment variables through your normal secret/configuration
mechanism:

| Variable | Value |
| --- | --- |
| `DATABASE_URL` | PostgreSQL database owned by this receiver, reachable at runtime |
| `GREGALE_WEBHOOK_SECRET` | Signing secret chosen for the app webhook subscription |
| `SLACK_WEBHOOK_URL` | Slack incoming webhook URL for the chosen channel; treated as a secret |
| `ISSUE_APP_ID` | App UUID whose deliveries may be forwarded |
| `ISSUE_APP_SLUG` | Matching app slug for dashboard links |
| `GREGALE_DASHBOARD_ORIGIN` | HTTPS dashboard origin, such as `https://gregale.dev` |
| `ISSUE_TEAM_ID` | Optional team UUID; omit to receive alerts for the whole app |
| `ISSUE_EVENTS` | Optional comma-separated event list; default described below |
| `PORT` | Optional listening port; defaults to 8080 |

```sh
npm --prefix examples/issues-slack run setup
npm --prefix examples/issues-slack start
```

The setup command creates only `gregale_issue_slack_receipts` and its index in
that database. It is not a Gregale control-plane migration. `/healthz` is a
liveness endpoint. Register an app webhook with your receiver's HTTPS
`/webhooks/gregale` URL, the same signing secret, and an explicit event filter:

```json
{
  "target_url": "https://YOUR_RECEIVER/webhooks/gregale",
  "webhook_secret": "YOUR_SIGNING_SECRET",
  "delivery_format": "json",
  "event_filter": ["issue.created", "issue.regressed", "issue.impact_threshold_reached"]
}
```

Use the existing dashboard Webhooks page or `POST /v1/apps/{slug}/webhooks`.
Configuring Gregale directly with the Slack URL will not work: Slack expects a
Slack payload, while Gregale delivers a signed Issues transition. The receiver
performs this conversion. Both standard JSON and CloudEvents deliveries are supported; the outer delivery ID must match the signed header. Receipt hashes use the stable issue transition, excluding the retry attempt counter. To enable assignment, resolve, reopen or ignore
notifications, include the event in **both** the subscription filter and
`ISSUE_EVENTS`. Available events are `issue.created`, `issue.regressed`,
`issue.impact_threshold_reached`, `issue.assigned`, `issue.resolved`,
`issue.reopened`, and `issue.ignored`. A team-specific receiver forwards events
whose captured `assignee_team_id` matches; it does not synchronize channel members
or notify the former team after reassignment.

## Package

After building the SDK and installing this example, run:

```sh
node examples/issues-slack/bundle.mjs
```

Deploy the printed fresh directory with the usual Node source-deployment flow,
or run it on another host. It contains the receiver, PostgreSQL setup script,
lockfile, and only the two built SDK modules it uses. Environment files,
credentials, tests and `node_modules` are excluded. Configure database access and
secrets in the destination, run `npm ci --ignore-scripts` and `npm run setup`, then
start with `npm start`. A durable PostgreSQL database is required; receipts do not
live on the app's ephemeral filesystem.

## Delivery behavior

Signatures are checked before parsing or database/provider work. Unsupported
or differently scoped events return 204 without sending. A successful Slack
send is acknowledged only after its receipt commits. Concurrent retries receive
503 while a delivery is leased; a failed send releases the lease, and an
interrupted receiver's lease can be reclaimed after 30 seconds. Slack calls time
out after five seconds and redirects are disabled. A completed receipt prevents
reposting the same delivery across restarts; a changed logical transition under the same
ID returns 409.

Slack incoming webhooks do not provide idempotency keys. If Slack accepted an
alert but its response or the receipt write was lost, retrying can produce a
duplicate. This bridge provides confirmed-delivery deduplication, not
exactly-once delivery. Keep receipt rows at least as long as your Gregale webhook
replay retention; deleting receipts permits old deliveries to be sent again.
Use the existing webhook delivery history, retry and replay controls to diagnose
provider failures. Receiver errors do not echo secrets or payloads.

Alerts include titles, environment/state, owner UUIDs, and a dashboard link.
They omit exception stacks, raw tags, request context and customer identities.
Names and titles are plain text; fallback Slack markup is escaped to avoid
turning issue content into mentions or links.

## Test locally

```sh
npm --prefix sdk/node run test:build
node --test sdk/node/dist-test/test/issue-slack.test.js
npm --prefix examples/issues-slack test
# Include persistent/concurrent/restart checks against a local test database:
ISSUE_SLACK_TEST_DATABASE_URL=postgres://... npm --prefix examples/issues-slack test
```

Tests mock Slack sends. PostgreSQL tests create and remove a uniquely named test
schema, leaving existing tables untouched.
