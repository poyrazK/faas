# ADR-20261028090000: PR previews get their configuration from the PR and from a preview scope

- **Status:** accepted
- **Date:** 2026-10-28
- **Owner:** apid (preview values) and githubd (PR head import) maintainers
- **Amends:** ADR-310 (previews never copy production values), ADR-20261026090000 (PR preview environments for general availability)
- **Decision:** A PR preview gets configuration from two sources. Neither copies a production value.
  1. **The PR's own Compose values.** When githubd applies a PR head to its previews, it imports the plain values the PR's Compose file declares into each preview's manifest environment. It uses the same opt-in (`x-gregale-env-import: true`) and rules as a project deploy (ADR-20261026181020): secret-looking values are skipped, and an address that calls a project service by its Docker name becomes that service's Gregale address, which a preview resolves to its PR sibling. githubd computes the values from the PR head on every push, so a removed value disappears with the next push. Platform-owned `GREGALE_SERVICE_*` keys are never overwritten.
  2. **The `pr-preview` scope.** Values and secrets set on a production app in the reserved scope `pr-preview` (`gregale env set --app shop-web --scope pr-preview KEY=VALUE`) become the default-scope values and secrets of every preview of that app:
     - **When.** apid applies them before it enqueues a preview build (the GitHub path) and when it creates or reopens a preview (`gregale preview create`). In the second case, a change invalidates the preview's snapshots so its next wake starts with the new values.
     - **Add and update only.** Keys in `pr-preview` are added to the preview, and changed values replace the preview's. Nothing else is removed: a value set directly on a preview keeps working unless `pr-preview` sets the same key, and a key removed from `pr-preview` stays on open previews until they are torn down or it is unset on them. Rotating a value, the usual reason to change a credential, reaches every preview at its next build.
     - **Managed credentials** (managed Postgres and object storage) are never copied and never removed.
     - **Sealing.** Secrets are copied sealed. Sealing binds the account and key, not the app, so apid never decrypts them.

  At runtime the existing order applies (`guest/init`: manifest < values < secrets), so `pr-preview` values override the PR's Compose defaults.

  **Reserved name.** `pr-preview` is reserved: `ValidProjectEnvironmentSlug` refuses it, so no new project environment can be named `pr-preview`. If a project already has an environment with that name, apid does not apply the scope to its previews and logs a warning. That environment's values could be production configuration.
- **Why:**
  - Previews were GA but started with no configuration. A service that needs `LOG_LEVEL`, an API base URL or a test key failed in every preview.
  - The documentation said to configure preview credentials "separately", but there was no lasting way to do it: a value set on a preview app belonged to that pull request only.
- **Consequences:**
  - **Quota.** `pr-preview` values count toward the production app's environment and secret quotas, because quotas count every scope. Each preview receives them in its own quota.
  - **Values set directly on a preview** (`--app pr-7-shop-web`) keep working. A `pr-preview` value with the same key replaces them at the preview's next build or reopen.
  - **Rollout.** An older githubd builds previews without the PR Compose values. An older apid ignores `pr-preview`, whose values sit unused on the production app.
  - **Ownership.** apid remains the only writer of app values and secrets. githubd writes only the preview's manifest environment, which it already owned for service bindings.
- **Rejected alternatives:**
  - **Inherit production values with an allowlist.** Any rule that reads production values can leak production credentials into code under review (ADR-310).
  - **Mirror the scope exactly, removing other keys.** That would silently delete values people already set directly on previews, and nothing records which keys a sync wrote.
  - **Resolve `pr-preview` at runtime from the parent app.** Secret delivery tracks versions, acknowledgements and revocations per app row. Reading another app's rows would need parallel delivery state; copying keeps one owner per row.
  - **Preview values in the repository** (`.gregale/project.yaml`). The repository cannot hold secrets, and test keys are usually secrets.
