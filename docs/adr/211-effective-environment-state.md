# ADR-211 · Effective project-environment state and safe diffs

- **Status:** accepted
- **Date:** 2026-09-22
- **Decision:** Gregale defines one effective-state read model for a registered
  project environment. It contains the immutable project configuration, each
  workload's selected live release, environment-scoped runtime variables,
  safe secret metadata, and managed-resource binding ownership. The API exposes
  that model at `GET /v1/projects/{slug}/environments/{environment}/state` and
  compares two models at the sibling `/diff?from=` endpoint. The existing
  config-only diff remains backwards compatible. `POST .../environments` with
  `from_environment` atomically creates the registry row and copies the latest
  configuration, runtime variables, and customer-managed secrets.
- **Secret safety:** State and diff responses never contain secret plaintext,
  ciphertext, or sealing-key identifiers. Customer secrets compare by their
  keyed value fingerprint. Managed credentials additionally compare binding
  ownership and credential generation. A row without a fingerprint is
  reported as `unknown`, never equal. An environment clone copies a sealed
  customer envelope only inside the control-plane transaction. Provider-issued
  credentials are never copied: cloned managed bindings receive fresh,
  environment-scoped credentials. By default, managed PostgreSQL is restored
  into a separate database and object storage is copied into a separate bucket.
  `share_resources` opts into fresh credentials over the source database or
  bucket, so the data remains shared.
- **Ownership (initial decision):** Domains, declared route structure, and edge policies were
  application-scoped when this ADR landed. The read model reported them as shared instead
  of pretending they are environment-owned or silently declaring them equal.
  Moving any of those resources into the cloneable set requires durable
  environment ownership and an additive follow-up API.
- **Artifact identity:** Release equality is based on the strongest available
  immutable artifact identity (image digest, source archive digest, commit,
  build, then deployment ID). Promotion may create a new deployment row for an
  unchanged artifact without producing a false artifact change.
- **Why:** Config-only comparison cannot explain staging/production drift in
  releases, runtime variables, credentials, or bindings. A single truthful
  model is also the prerequisite for a safe `environment create --from`
  transaction and for first-class preview environments.
- **Consequences:** The project diff CLI now uses the unified endpoint. Runtime
  variable values are visible on this MFA-gated operator surface, matching the
  existing app env-diff contract. The clone checks the existing cross-scope
  per-app secret and variable quotas before writing and rolls back every target
  row on any failure. PostgreSQL isolation requires provider point-in-time
  restore support and consumes a database from the account quota. Object-storage
  isolation requires cross-bucket copy support and is not an atomic snapshot
  while the source is being written. Deleted environments durably queue managed
  resource cleanup and retry it after transient provider failures. Secret
  versions for customer-managed secrets remain a
  follow-up because the current schema stores fingerprints and update timestamps
  but no monotonic version. This ADR adds no database migration.
- **Rejected alternatives:** Joining the existing config and per-app diff calls
  in the CLI would produce a torn snapshot and duplicate policy in every
  client. Comparing sealed ciphertext is invalid because age encryption is
  nondeterministic. Treating application-global routes, domains, and policies
  as environment resources would make clone output look safer than it is.

## Follow-up: environment-owned declared routes (ADR-233)

Registered environments can now own an explicit per-workload declared-route
contract. The route-policy PUT endpoint replaces that contract, clone snapshots
it atomically, and state/diff report both the contract and its ownership.
The CLI exposes the write as
`gregale projects environments routes set <project> <environment> <workload> --file routes.json --yes`.
Deployment-specific URLs enforce the scoped contract; the ordinary application
hostname continues to use the application-wide contract. An application-wide
OpenAPI document is not copied: when enforcement depends on one without an
explicit route list, clone leaves routes shared and reports that limitation.
Domains and edge rules remain application-scoped; they are not part of this
route-policy follow-up.
