# GitHub App configuration

Push-to-deploy, PR previews, Check Runs and PR comments all depend on one
GitHub App (production: `gregale-platform`, App ID 4742036). Its settings live
on GitHub, outside this repository and outside every deploy, so a wrong value
breaks every customer without any Gregale change. In October 2026 the webhook
URL pointed at the marketing site and the secret had drifted; no webhook
reached githubd for weeks and nothing alerted. This page is the contract; the
`FaasGithubApp*` alerts ([runbook](../runbooks/FaasGithubAppWebhook.md)) catch
drift from it.

## Webhook

| Setting | Value |
|---|---|
| Active | on |
| Webhook URL | `https://api.gregale.dev/webhooks/github` (the platform API host of the environment, path `/webhooks/github`) |
| Content type | `application/json` |
| SSL verification | enabled |
| Secret | identical to `FAAS_GITHUB_WEBHOOK_SECRET` in `/etc/faas/secrets/githubd/githubd.env` and `/etc/faas/secrets/gatewayd-internal/gatewayd-internal.env` |

`gatewayd-public` routes exactly `/webhooks/github` on the platform API host to
githubd's loopback listener. Any other host (the marketing site, a preview
host, a trailing-dot hostname) answers 404 and GitHub records a failed
delivery.

The secret is App-wide: GitHub signs every installation's deliveries with it.
Change it only with [GithubWebhookSecretRotation](../runbooks/GithubWebhookSecretRotation.md),
which updates both daemons and the App together. githubd answers a delivery
signed with any other secret with 401 and counts it as
`githubd_ops_total{op="webhook_push",code="signature_invalid"}`.

## Events

| Event | Needed for |
|---|---|
| Pull request | PR previews: create on open and reopen, rebuild on push, tear down on close |
| Push | Push-to-deploy on the production branch, deployment skips, CI sources |
| Repository | Keeping bindings when a repository is renamed, transferred, archived or deleted. Without it a rename detaches the binding at the next installation sync. |
| Issues | Only for issue-tracker callbacks (ADR-1476) |

GitHub sends installation events (`installation`, `installation_repositories`)
to every App without a subscription. githubd answers any other subscribed event
with `202 {"status":"ignored"}`, so extra subscriptions are harmless but
useless.

## Permissions

| Permission | Access | Used for |
|---|---|---|
| Checks | Read and write | `gregale` and `gregale-preview` Check Runs |
| Contents | Read-only | Fetching source at the pushed commit, compare API for path filters |
| Deployments | Read and write | GitHub deployments and their statuses, including each preview's environment link |
| Issues | Read and write | The preview status comment on the PR (PR comments use the Issues API), issue-tracker callbacks |
| Metadata | Read-only | Required by GitHub for every App |
| Pull requests | Read-only | Current PR head checks before acting on a delivery |

Adding a permission makes GitHub ask every installation's owner to approve it;
until they do, those installations keep the old permission set.

Without Deployments access, GitHub answers 403 to every deployment write.
githubd skips the GitHub deployment and still writes the Check Run and the PR
comment, so the only loss is the PR's environment link. Each skip counts as
`githubd_ops_total{op="github_deployment",code="forbidden"}` and logs
`githubd: GitHub refused a deployment write` at most every 10 minutes.

## Credentials on the control plane

| Value | Where |
|---|---|
| App ID | `FAAS_GITHUB_APP_ID` |
| Private key | systemd credential of `faas-githubd.service` |
| OAuth client ID and secret | `FAAS_GITHUB_APP_CLIENT_ID`, `FAAS_GITHUB_APP_CLIENT_SECRET` |
| Webhook secret | `FAAS_GITHUB_WEBHOOK_SECRET` |

githubd also uses the private key to read the App's delivery log for the
`FaasGithubApp*` alerts.

## Check after any change

1. In **GitHub App → Advanced → Recent Deliveries**, redeliver a recent `push`
   or `pull_request` delivery and confirm `202`.
2. Within five minutes `githubd_github_hook_latest_delivery_failed` reads 0.
3. `gregalectl github status --status dead` shows nothing new.
