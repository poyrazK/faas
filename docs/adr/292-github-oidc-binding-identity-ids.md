# ADR-292 · Bind GitHub OIDC to immutable repository identity

- **Status:** accepted
- **Date:** 2026-09-28
- **Related:** ADR-366 (immutable GitHub Actions OIDC subjects)
- **Decision:** When a user binds an app to a GitHub repository, githubd resolves that repository through the selected GitHub App installation and persists its owner ID and repository ID with the repository name. First-use OIDC bootstrap for an immutable subject requires the signed subject's owner ID and repository ID to match the persisted binding and the repository name. A binding without IDs or with different IDs does not bootstrap an account.
- **Why:** ADR-291 preserved IDs in the signed trust policy but discarded them while selecting the account. A repository name can be reused, so matching the name alone can select a stale binding for a different GitHub repository identity.
- **Consequences:** New bindings record the IDs returned by GitHub's installation repository API. Existing bindings with no IDs continue to support legacy name-only subjects; immutable-subject deployments require the repository to be rebound through its GitHub App installation so the IDs are captured. Repository transfers that change the owner ID likewise require an explicit rebind. Rename events retain the stored IDs.
- **Rejected alternatives:** Backfilling existing bindings by name could attach an old binding to a different repository after a name is reused. Accepting a missing or mismatched ID would discard GitHub's immutable identity signal.
