# Build secrets resolve like the runtime environment

Date: 2026-10-24
Status: Accepted
Amends: ADR-20261022185329 (Compose build secrets)
Related: ADR-423 (secret aliases), ADR-1139 (project environment secrets), ADR-20261022111812 (seal domains)

## Context

ADR-20261022185329 mounts a Compose `build.secrets` entry whose top-level secret
is declared with `environment: NAME`. builderd looked `NAME` up among the
app's own secrets in the deployment's scope and nothing else, so two sources the
app does read at runtime were missing from builds:

- an environment reference (alias, ADR-423) that gives the variable `NAME` the
  value of another secret, such as `NPM_TOKEN → secret:CI_NPM_TOKEN`;
- a project environment secret the app inherits (ADR-1139), set once for every
  service in the environment.

A build that named either failed with "not set", although the same app saw the
value as `NAME` when it ran.

## Decision

A build secret named `NAME` receives the secret the deployment's serving
process receives as the environment variable `NAME`.

- **One resolution rule.** builderd reads the deployment's runtime values
  snapshot (`RuntimeAppValuesForDeployment`) and its environment secret intent,
  the reads schedd uses for a wake, and resolves with
  `state.ResolveRuntimeSecretSources`. That follows the wake's order: the
  deployment's explicit secret grants, or every scoped app secret when it has
  none; then the environment's references and suppressions; then, only without
  explicit grants, the project environment's secrets for keys the app does not
  set. A release-only credential (a managed PostgreSQL migration secret) is
  refused, as for any serving process. builderd cannot link the scheduler, so
  a scheduler test checks that the shared resolver gives every key the
  ciphertext and source key the wake delivers, and nothing it leaves out.
- **Ciphertext and source key only.** An alias or project secret travels to
  vmmd as an alias-form sealed entry (`key` is `NAME`, `source_key` the sealed
  secret's key), as on a wake. builderd still holds no key and opens nothing;
  vmmd, the `runtime` domain's reader, opens it into the builder's drive under
  `NAME`, so the guest side is unchanged.
- **Alias fence.** Before staging a build that carries such an entry, builderd
  asks vmmd whether it supports secret aliases and refuses the build if it does
  not. After the boot it checks the acknowledgement and destroys a builder whose
  vmmd was replaced in between, as schedd does for a wake.
- **Errors.** A name that resolves to nothing is reported as before, by name and
  scope. A release-only credential is the customer's to fix and fails the build
  as a user error.

## Consequences

- Builds read the same secrets the app does: aliases, environment suppressions,
  explicit grants and inherited project secrets apply to both.
- builderd now reads the runtime values snapshot. That read includes plaintext
  environment variables, which builderd ignores, and secret ciphertext it never
  opens. Every runtime database role can read those tables today
  (ADR-20261023104734); a narrower builderd grant must include them.
- Release skew: a vmmd that predates alias support refuses these builds at the
  fence. A plain same-name secret still uses the legacy envelope contract.
- Native acceptance (a private dependency installed through an aliased and a
  project secret) is pending with the other container lanes.
