# ADR-20261110180000: Declared bindings gate checks, manifest drift warnings and session connection budgets

- **Status:** accepted
- **Date:** 2026-11-10
- **Owner:** CLI and managed PostgreSQL maintainers
- **Amends:** [ADR-20261110120000](20261110120000-binding-review-rounds-7-11.md) (session role connection limit), [ADR-20261106105015](20261106105015-declarative-resources-connect-and-lifecycle-helpers.md) (gregale.yaml databases)
- **Context:**
  - `gregale app binding check` passed, with only a `no_bindings` warning, for an app whose `gregale.yaml` declares a database but has no binding. `--require-bindings` blocks that, but it is opt-in, and it accepts any one binding when the manifest declares several.
  - Removing a `databases` entry from `gregale.yaml` never unbinds the database, by design: a deploy that revoked a binding would revoke the credential the serving release uses. Nothing told the customer the manifest and the app had drifted.
  - The connection budget counted `session` bindings under `other_bindings` and reported `client_pool_sizes_unknown`, so every database with one never reached `within_budget`. Each session role now carries `CONNECTION LIMIT 42`, so its ceiling is known.
  - The object-storage compute binding list and the developer bucket list issued one rotation query per binding.
- **Decision:**
  1. **Declared bindings in the check.** `bindings check` reads `./gregale.yaml` and requires every `databases` entry declared for the checked app: one naming the app with `app:`, or one without `app:` when the directory is linked to that app (`gregale link`). An entry with an explicit `scope` applies only to that scope; one without follows the deployment's scope, as on deploy.
     - A declared binding the inventory does not list is the blocker `declared_binding_missing`.
     - Declaring any binding makes zero bindings a blocker (`no_bindings`), as `--require-bindings` does.
     - `--ignore-manifest` checks without the file. A manifest that cannot be read fails the check rather than being skipped.
     - Buckets are not covered yet: their binding identity is the secret prefix, which a manifest may leave to the server.
  2. **Drift warning on deploy.** After applying `databases`, the CLI lists the app's live PostgreSQL bindings and warns about each one, in a scope the manifest binds, whose environment key no entry declares. The warning names `gregale postgres bindings delete <id>`. Deploy still never unbinds. The listing is best-effort: a failure leaves the deploy unaffected. A manifest with no `databases` entries for the app is not compared, so removing the last entry is not reported.
  3. **Session bindings in the connection budget.** `session` bindings get their own `inventory.session_bindings` and `demand.session_connection_ceiling` (`api.SessionBindingConnectionLimit` per binding, twice that while a binding rotates, since the old and new role can both hold connections). The ceiling is part of `modeled_peak_connections`, and session bindings no longer count in `other_bindings` or cause `client_pool_sizes_unknown`.
  4. **One rotation read per bucket.** `PendingObjectS3CredentialRotations` returns the pending rotations of all of a bucket's bindings in one query.
- **Consequences:**
  - A CI job that runs `bindings check` from a repository whose `gregale.yaml` declares a database the app does not have now fails. That is the point; `--ignore-manifest` restores the old behaviour.
  - Release skew: an older CLI validates `modeled_peak_connections` as the sum of the fields it knows, so it rejects a budget with a non-zero session ceiling. Only databases with `session` bindings are affected, and `session` stays off until an operator sets `session_enabled`. Ship the CLI before enabling it.
  - The new JSON fields are `omitempty`; the SDKs are regenerated.
