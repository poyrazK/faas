# ADR 20261023173624 · Runtime secrets are bound to their account

- **Status:** accepted (2026-10-23)
- **Date:** 2026-10-23
- **Related:** ADR-020 (customer secrets), ADR 20261022111812 (seal domains),
  ADR-1139 (release secret rekey documents), ADR 20261023104734 (runtime
  database roles)

## Context

A runtime secret is an age envelope holding `{KEY: value}`. vmmd opens it at
wake with the runtime identity and writes the values into the guest. The age
header says *who* can decrypt an envelope. Nothing says *for which tenant*:
vmmd decrypts whatever ciphertext the wake request carries. Copy another
account's `app_secrets.ciphertext` into your own app's row, or misroute it
through a clone, release or promotion bug, and vmmd hands that tenant's
secret to your VM. Seal domains narrowed who can decrypt runtime secrets.
They did not narrow which workload one may be decrypted into. vmmd's existing
check (an envelope must hold exactly its selected key) only stops key
renaming.

What a binding can name is set by how ciphertext moves. apid cannot open
runtime secrets since seal domains, so every copy moves bytes unchanged.

- **Project environment secrets** are sealed once per environment and handed
  to every app in it. An app id cannot be the binding.
- **No path crosses accounts.** The paths that copy within an account are
  environment clones, release snapshots, promotions, config guard restores,
  sidecar inputs, GitOps candidates, build and job secrets. No path moves an
  app or its secrets to another account: ownership transfer changes
  `org_memberships`, not `apps.account_id`.
- Every wake, cold boot, job and build request already carries the account
  id; the wake protos reject an empty one.

## Decision

1. **Bound envelopes.** `secretbox.SealBound` writes
   `{"v":1,"binding":{"account_id":…},"values":{…}}` inside the age payload.
   The binding is encrypted and authenticated with the values, so it cannot
   be edited without the runtime identity. A zero binding writes the original
   format byte for byte.
2. **Readers accept both formats.** `OpenMulti` opens either and drops the
   binding. `OpenMultiBound` returns it. A release from before the binding
   decodes plaintext into `map[string]string`, which fails on `"v":1`, so it
   refuses a bound envelope instead of misreading it. A legacy envelope cannot
   carry `v`: keys are upper-case env names.
3. **vmmd enforces it.** Every path that opens runtime secrets for a workload
   checks the binding against the request's account and refuses a mismatch or
   an empty account. Those paths are wake and cold boot, jobs, builds,
   sidecar app secrets and live reload (`UnsealRuntimeSecrets`). Unbound
   envelopes still open until the contract step.
4. **Re-sealing keeps it.** The rekey runner, release-snapshot rekey,
   `gregalectl seal-domain migrate` and `gregalectl fleet-seal migrate` carry
   the binding into the new envelope.
5. **Writers bind behind a unit switch.** `FAAS_RUNTIME_SECRET_BINDING` (`off`
   by default, or `account`) is set by the seal_domains role. It is unit
   configuration, not runtime configuration, so a database writer cannot
   switch it off. Under it, apid binds app secrets (set and rotate) to
   `apps.account_id`, the value schedd sends vmmd. It binds project secrets,
   object-storage binding values, release bundles, managed Postgres
   credentials and clone-checkpoint `DATABASE_URL`s to their account.

### Rollout

- **Expand (this change).** Readers check bindings and writers stay `off`.
  Nothing changes on disk.
- **Bind new secrets.** Once every vmmd and gregalectl run this release and
  the previous release has left the rollback window, set
  `faas_seal_domain_runtime_binding: account` with
  `faas_seal_domain_runtime_binding_readers_verified: true`.
- **Bind stored secrets (follow-up).** `gregalectl seal-domain migrate` gains
  a binding pass. It re-seals each stored envelope bound to the account of
  the row or document that owns it. It runs as an operator command, as the
  domain re-seal does, because only a holder of the runtime identity can
  re-seal.
- **Contract (follow-up).** vmmd refuses unbound runtime envelopes once the
  count reaches zero.

## Consequences

- Moving ciphertext across accounts, by an attacker who can write secret
  rows or by a bug in a copy path, now fails closed at wake. It no longer
  decrypts into the wrong tenant.
- It does not stop a writer who can also change ownership rows (pointing an
  app at another account) or redirect another app's deployment. Those need
  the database write itself, which the runtime database roles step narrows.
  With narrowed roles, a secret-row write alone is no longer enough.
- An app-level binding would be stronger. It needs project secrets to bind
  to their environment and job and build requests to carry an app id, which
  they do not today. That is left for later.
- Sidecar direct env (`SealBytes`, namespace `sidecar_env`) uses the byte
  format and is not bound yet.
- Unbound rows keep working until the binding pass and the contract.
