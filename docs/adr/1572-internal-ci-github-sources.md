# ADR-1572: Account-owned GitHub sources for blocked CI admission

- Status: accepted for internal qualification; disabled by default
- Date: 2026-10-15
- Extends: ADR-1576 (CI control plane), ADR-1571 (execution qualification)

## Decision

Add GitHub repository bindings, an authenticated trigger journal, and a
separate Checks outbox. Production compute remains disconnected. The execution
qualification ledger receives no GitHub events; all admitted GitHub runs are
ordinary `blocked` receipts with `compute_unavailable`.

Preserve component ownership: githubd verifies the App webhook HMAC and writes
compact source evidence; apid owns bindings and creates CI run intent. An apid
worker revalidates account and repository access over the existing githubd RPC
surface. githubd alone calls GitHub to publish Checks. No new public listener,
direct VM call, source checkout, or command execution is added.

## Gates and authority

The existing exact-`1` internal flag and account UUID allowlist remain required.
`FAAS_CI_GITHUB_ENABLED` separately gates binding routes, authenticated trigger
capture and admission. `FAAS_CI_GITHUB_CHECKS_ENABLED` additionally gates outbound
Checks. Both new flags default to `0`; generated apid and githubd units retain
all CI flags off and the allowlist empty. Each daemon reads its policy at boot.
Turning on metadata alone cannot admit webhooks or publish Checks.

Binding mutations require an authenticated active account, admin scope and the
existing MFA gate. apid checks durable ownership of the exact installation and
the live GitHub repository catalogue, matching repository ID and full name
against the selected immutable pipeline revision. A repository descriptor alone
does not grant authority. Bindings are separate from deployment/app bindings.

Every trigger admission and Check delivery revalidates durable installation
ownership and current remote access; a cached installation token is insufficient
proof. Inactive or abuse-held accounts are refused. Access loss suppresses work;
temporary provider or database failures retry with bounds. App suspension,
uninstallation, repository removal, rename or transfer cannot authorize work
through stale names alone. Changed repository identities require an explicit
binding update and verification.

CI trusts only deliveries verified with the GitHub App-level webhook secret.
The existing legacy installation-scoped signing fallback remains outside CI;
such a signer cannot supply authoritative identities for another installation.

## Trigger selection and durability

Bindings select an explicit revision UUID, installation ID, immutable repository
ID, and nonempty exact branch filters. Push filters apply to branch names; PR
filters apply to base branches. No implicit all-branch default, wildcard filters,
path filtering, remote pipeline configuration or secret injection is introduced.

Accept branch pushes and open, non-draft same-repository PR events (`opened`,
`reopened`, `synchronize`, `ready_for_review`). Pin push `after` or PR head SHA.
Require positive repository/installation IDs and matching complete PR base/head
repository identities. Forks, missing identities, closed/draft PRs, deletion
sentinels, tags and other ref types do not create CI work.

After durable insertion in the existing authenticated GitHub inbox, the receiver
records compact CI trigger snapshots before acknowledgement. The snapshot
retains the binding generation, selected revision, exact source identity and
first delivery ID. It contains no raw webhook body, credentials or command
content. If journal insertion fails, the receiver returns 503. A retry repeats
the idempotent insertion even when the deployment inbox already has the delivery.
These two inbox writes are separate transactions; acknowledgement follows both.

Deduplicate by account, binding and a hash of event kind, installation,
repository identity, commit SHA, branch/base branch and PR number. Changing the
delivery ID or repeating an action on the same PR head cannot mint another run.
Different PRs, branches and trigger kinds remain separate events. The first
accepted snapshot wins; editing a binding during a retry cannot switch its
revision. An explicitly disabled or retargeted binding cannot admit old work.

apid claims a trigger with a bounded lease and revalidates access. Under an
account row lock, admission atomically creates the blocked run, source
provenance, lifecycle event and Checks outbox row, and completes the trigger.
Retries recover the original receipt before quota checks. Run caps apply to
manual and GitHub receipts together. No old blocked receipt is promoted.

## Checks delivery

Checks use a binding-specific `gregale-ci / <pipeline> / <binding UUID>` name and
a `gregale-ci:<run UUID>` external ID. They never reuse deployment Check mappings.
The only published result is `completed` / `action_required`, titled
`Gregale CI blocked: compute unavailable`. It records admission, not execution
success. This Check is an admission receipt; it does not track subsequent
metadata cancellation as execution lifecycle progress.

Before POST, discover an existing Check for the exact commit, name and external
ID. A lost creation acknowledgement is recovered by PATCHing that identity.
Discovery is paginated and bounded; exhausting the bound fails without another
POST. Check delivery is at least once: GitHub does not provide an atomic create
idempotency key, so an arbitrarily paused worker or remote consistency delay can
still produce duplicates. Local leases and external identity reconciliation
reduce that risk; they do not establish exactly-once external publication.

Separate trigger and Check leases last 60 seconds. Provider calls are bounded
to 15 seconds; failed work retries at most eight times with capped backoff.
Expired leases reject late admission/results. Disabling a binding suppresses
queued and leased work; already in-flight provider requests may finish.
Publication remains independently gated and accepted outbox entries can wait
until an operator deliberately enables it. Flag-off ingress creates no CI
journal entries to replay automatically later.

## Persistence, bounds and evidence

An append-only migration adds account-owned bindings, trigger snapshots, run
source metadata and Check delivery records. SQL is generated through the CI
SQLC projection. State columns have CHECK constraints, ownership FKs cascade on
account deletion, and account locks serialize binding/inbox/run quotas.
Installation rows are not FK parents of retained CI history: live authority is
rechecked instead of deleting receipts on uninstall.

All bounds are in `pkg/api/limits.go`: 16 bindings and 256 retained triggers per
account, 16 exact filters per event type, 255 bytes per branch, 64 KiB snapshots,
eight attempts, a 16-item reconciliation batch and bounded Check discovery.
Disabled bindings and terminal triggers retain slots. This internal release
adds no pruning, retention promise or dead-letter redrive API.

Memory and PostgreSQL scenarios cover concurrent deduplication, pinned revision
snapshots, stale-worker fencing, restart recovery, access loss, separate gates,
caps, disablement and atomic admission. HTTP tests prove HMAC-before-journal and
signed redelivery producing one blocked receipt. A synthetic GitHub API verifies
Checks identity recovery, action-required results, installation isolation and
retryable rate limits. No live Check publication or compute qualification is
claimed.

The next compute milestone remains a scheduler-owned Jobs adapter with source
checkout, resource/disk admission, image qualification, scoped secrets/caches,
artifact storage, accounting and native cleanup acceptance behind a separate
disabled dispatch gate.
