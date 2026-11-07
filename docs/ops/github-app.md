# GitHub App configuration

Push-to-deploy, PR previews, Check Runs and PR comments all depend on one
GitHub App (production: `gregale-platform`, App ID 4742036). Its settings live
on GitHub, outside this repository and outside every deploy, so a wrong value
breaks every customer without any Gregale change. In October 2026 the webhook
URL pointed at the marketing site and the secret had drifted; no webhook
reached githubd for weeks and nothing alerted. This page is the contract; the
`FaasGithubApp*` alerts ([runbook](../runbooks/FaasGithubAppWebhook.md)) catch
drift from it.

## Deployment connections and sign-in

The same GitHub App installation may be authorized independently by multiple
Gregale accounts for deployments. Each must complete Connect GitHub and pass
the personal-owner or organization-admin checks. Repository access and project
ownership remain scoped to the Gregale account. GitHub sign-in identities
remain associated with exactly one Gregale account; deployment connections do
not move those identities. Uninstalling or suspending the App on GitHub revokes
every Gregale connection derived from that installation. See
[ADR-20261106203000](../adr/20261106203000-shared-github-deployment-connections.md)
for migration and rollout requirements.

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
| Repository | Keeping bindings and projects when a repository is renamed, transferred or deleted. Without it a rename reaches Gregale only at the next hourly installation sync, which matches by repository ID, and pushes in between are not deployed. |
| Issues | Issue-tracker callbacks (ADR-1476) |

GitHub sends installation events (`installation`, `installation_repositories`,
and `installation_target` when a user or organization is renamed) to every App
without a subscription. githubd answers any other subscribed event with
`202 {"status":"ignored"}`, so extra subscriptions are harmless but useless.

`FaasGithubAppConfigDrift` fires when GitHub reports the App without one of
these events or one of the permissions below (ADR-20261101160300).

## Permissions

| Permission | Access | Used for |
|---|---|---|
| Checks | Read and write | `gregale` and `gregale-preview` Check Runs |
| Contents | Read-only | Fetching source at the pushed commit, compare API for path filters |
| Deployments | Read and write | GitHub deployments and their statuses, including each preview's environment link |
| Issues | Read and write | The preview status comment on the PR (PR comments use the Issues API), issue-tracker callbacks |
| Metadata | Read-only | Required by GitHub for every App |
| Pull requests | Read-only | Current PR head checks before acting on a delivery |
| Organization → Members | Read-only | Connect GitHub checks that whoever connects an organization's installation is an active admin of it |

## Connect GitHub URLs

| Setting | Value |
|---|---|
| Callback URL | `<console origin>/oauth/code-callback`: apid's `cli_auth_url_base` (`FAAS_CLI_AUTH_URL_BASE`) plus the path, or `FAAS_GITHUB_APP_REDIRECT_URI` when set. The two must match exactly. |
| Request user authorization (OAuth) during installation | on |
| Setup URL | `<console origin>/oauth/callback` (used only when the option above is off) |
| Redirect on update | on |

Connect GitHub sends the customer to GitHub's user authorization. When the
GitHub user has no installation of the App, Gregale sends them to install it;
with user authorization during installation on, GitHub returns to the
Callback URL and the connection completes in one pass. Both callbacks work for
every Gregale sign-in provider: GitHub proves that the authorizing user owns
the personal installation or administers the organization.

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


### Account connections and missed deliveries

Customers manage deployment connections in Settings → Integrations. “Verify
access” shows the installation owner type, repository selection, current
permission grants, repository count, and verification time. Apps can select a
personal or organization installation independently. “Disconnect” there removes
only the signed-in Gregale account's access; uninstalling the App on GitHub
still revokes every Gregale connection to that installation.

The monitor automatically repairs recent transport failures through GitHub's
[App webhook redelivery endpoint](https://docs.github.com/en/rest/apps/webhooks#redeliver-a-delivery-for-an-app-webhook).
It scans at most 500 attempts from the past hour, waits two minutes, and caps
redelivery at three attempts per GUID with backoff and ten requests per poll.
Already accepted inbox events retain their normal worker retry and dead-letter
policy. A 401/403/404 delivery needs configuration or permission repair; it is
not automatically replayed. A provider rate-limit response pauses all repair
requests using durable state. The existing delivery gauges and worker logs
remain the operator diagnostic surface.

GitHub check names now include a stable account suffix. Update repository
branch-protection required checks to the intended account's new name after the
first deployment. Do not use another Gregale account's check as the required
status. Apply the expansion and batched backfill first; activate sharing only
in a full release after every githubd worker runs the account-aware code.
