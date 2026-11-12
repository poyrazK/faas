# Deploy from a GitHub source ref: operator notes

Moved out of the customer page `docs/source-ref.md` (ADR-20261028200304).
Design rationale: ADR-092 and ADR-093.

In split-box fleets, compute-side imaged performs the final source check over
the private `githubd.faas:50053` mTLS route provisioned by the manifest and
Ansible. The API route's auth chain is `authLimited → requireMFA →
requireScope(ScopesDeployWriteSurface) → idempotent → handler`; the Go SDK
binding is `pkg/api.Client.DeployFromSourceRef`.

## Webhook secrets (push-to-deploy)

The push-to-deploy loop is wired end-to-end. GitHub App webhook
deliveries are signed with the App's single webhook secret, so both
`gatewayd-internal` and `githubd` must receive the same
`FAAS_GITHUB_WEBHOOK_SECRET`. `githubd` verifies the signature before
persisting the delivery to its durable inbox and acknowledging GitHub;
the worker then routes `push` and `pull_request` events asynchronously.

The older installation-scoped secret API remains only as a compatibility
fallback for non-GitHub senders that provide an explicit installation
header. It is not the normal GitHub App delivery path.


## Related

- `docs/adr/092-headless-source-ref-deploy.md` — design rationale.
- `docs/runbooks/GithubWebhookSecretRotation.md` — operator rotation flow.
- `docs/cli-setup.md` — shell completion + man page install.
