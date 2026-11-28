# ADR-20261128195037: One variable model, sealed version history, and `env exec`

- Status: Accepted (owner decisions 2026-11-28)
- Date: 2026-11-28
- Amends: ADR-020 (customer secrets: key shape), ADR-045 (mutable app env),
  ADR-1136 (project environment config), ADR-1139 (project environment
  secrets), ADR-1451 (config history and guard: secrets become restorable)
- Related: ADR-089 (rotation), ADR-339 (runtime secret revocation),
  ADR-20261019120021 (envelope rotation and key retirement),
  ADR-20261110210000 (customer secrets open only on the control plane),
  PR #8225 (`env push` seals scanner hits instead of dropping them)

## Context

An app's configuration lives in two stores that the CLI presents under one
command, `gregale env`:

| | sealed secrets (`app_secrets`) | plain variables (`app_envs`) |
|---|---|---|
| written by | `env set`, `env push`, `env rotate` | `env set --plain` only |
| key shape | `^[A-Z][A-Z0-9_]*$` | `^[A-Za-z][A-Za-z0-9_]*$` |
| readable | never | yes |
| history | revision records the value hash; rollback cannot restore | revision records the value; rollback restores |

At project scope there are two more: `environment config set` (a whole JSON
document, replaced on every write, plain, refuses secret-shaped names, outside
config history and the guard) and `environment secrets set` (sealed).

The result, seen in support and agent evals:

1. `docs/env.md` presents `env push` as the tool for non-secret settings such
   as `LOG_LEVEL`, but push seals everything, so those values can never be read
   back or diffed.
2. A mixed-case key (`DOTNET_gcServer`, `next_public_x`) works with `--plain`
   and fails with the default `env set` and with `env push`.
3. The same key can exist in both stores; the sealed one silently wins.
4. `env rollback` restores variables but only lists the secrets that differ.
   The config guard keeps replaced ciphertext (`app_config_guard_secrets`) only
   while it is open, so a bad rotation found an hour later cannot be undone.
5. Sealed values are write-only. A developer who wants to run the app locally
   against the `development` environment must keep a separate copy of every
   value; `env pull` writes empty `KEY=` lines.

## Decision

### 1. One variable model

Every configuration value is a **variable** with a kind: `sealed` or `plain`.

1. **Default sealed.** `env set`, `env push`, `environment env set` and
   `environment env push` seal unless `--plain` is given. `--plain` is the only
   way to write a readable value. (Owner decision: sealed unless `--plain`.)
2. **One key shape** for both kinds: `^[A-Za-z][A-Za-z0-9_]*$`, ≤ 128 bytes.
   `api.SecretKeyPattern` and every database CHECK that pins the uppercase form
   (`app_secrets`, `project_environment_secrets`, `app_config_guard_secrets`,
   `app_environment_secret_refs`, `app_secret_revocations`, and the three SQL
   functions that filter on it) move to the mixed-case form. Existing keys are
   unaffected; nothing is renamed. (Owner decision: mixed case for both.)
3. **One kind per key per scope.** Writing a key in the other kind replaces it:
   the old row is deleted and the new one written in one transaction, recorded
   as one config revision (`source = env.kind_change`). The API refuses an
   implicit change with `409 env_key_kind_conflict` unless the request sets
   `replace_kind: true`; the CLI sets it after saying
   `LOG_LEVEL was sealed; it is now plain`. A migration resolves existing
   duplicates by keeping the sealed row (today's effective value) and deleting
   the plain one, recorded as a revision.
4. **`--plain` refuses credentials.** With `--plain`, a value the secret
   scanner matches is refused for that key with a hint to drop `--plain`.
   Without `--plain` scanner hits are only notes, because the value is sealed
   (PR #8225).
5. **One listing.** `env list` prints one table: key, kind, source (app,
   project environment, binding), value for plain keys, delivery status for
   sealed keys. `--json` gains `kind` and `source` and keeps every existing
   field.
6. **Project environments use the same verbs.** `gregale environment env
   set|push|unset|list <project> <environment>` write key by key, sealed by
   default, `--plain` into the configuration document. `environment config set`
   keeps its whole-document form for JSON-only keys. Project configuration
   changes are recorded in the project environment's config versions and listed
   by `environment env history`.

### 2. Sealed version history

1. **Table `app_secret_versions`** (and `project_environment_secret_versions`):
   `(account_id, app_id | project_id+environment_slug, scope, key, version,
   ciphertext, kid, value_hash, secret_class, replaced_at, replaced_revision)`.
   Each set, rotate, unset, kind change or guard restore that replaces or
   removes a sealed value writes the outgoing ciphertext here in the same
   transaction. No plaintext is ever opened to do this.
2. **Retention:** the last 10 previous versions of each key, and none older
   than 30 days, whichever limit is hit first. A sweeper in apid's maintenance
   loop deletes expired rows hourly. (Owner decision: 10 versions / 30 days.)
3. **Rollback restores secrets.** `env rollback <revision>` looks up, for every
   sealed key that differs, the version whose `value_hash` matches the target
   revision and writes that ciphertext back. Keys whose version has expired are
   listed as today, with a `gregale env set` command. Restored values go through
   the normal delivery fence, snapshot invalidation and the guard.
4. **`env rotate --undo KEY`** restores the newest retained version of one key.
5. **Revocation purges history.** A runtime revocation (ADR-339) or
   `env unset --purge-history` deletes every retained version of that key, so a
   leaked value cannot be restored and does not stay stored for 30 days.
   App deletion and purge delete the versions with the app.
6. **Key retirement covers history.** Envelope rotation
   (ADR-20261019120021) rewraps `*_secret_versions` with the live rows, so a
   retired `kid` never blocks a rollback.
7. Platform-managed credentials (rows carrying a managed Postgres or object
   storage binding) are not versioned: their source of truth is the binding
   generation.
8. The guard keeps its own `app_config_guard_secrets` until slice 4 moves guard
   restore onto version history.

### 3. `gregale env exec`

```bash
gregale env exec --env development -- npm run dev
gregale env exec --app api --env staging -- python manage.py shell
```

1. The CLI asks apid for the effective environment of the app (or linked
   project service) in that scope, then runs the command with that environment
   merged over its own (runtime precedence: manifest, project configuration,
   app plain variables, sealed values). Values exist only in the CLI's memory
   and the child's environment; no file is written and nothing is cached.
2. **Production is refused**, by apid and again by secretd: the default
   scope, `production` (`state.IsProductionScope`), and any project environment
   with `protected = true`. `403 env_exec_production_refused`. (Owner decision:
   non-production only.)
3. **Caller:** a human session with MFA verified in the last 15 minutes and the
   `secrets:read_values` permission, which owners and admins hold and members
   get only when granted. API keys, deploy tokens, agent connections and
   support grants are refused.
4. **Route:** `POST /v1/apps/{slug}/env/exec` with `{scope}` returns
   `{variables: {KEY: value}, kinds, expires_at}` with `Cache-Control:
   no-store`. apid obtains sealed values from secretd through a new
   `OpenAppSecretsForDeveloper` RPC that re-checks scope protection against
   the database, so the open stays on the control plane
   (ADR-20261110210000).
5. **Audit:** `secrets.exec_opened` with the app, scope, key names (never
   values), session and client IP; the account's security log shows it.
6. **Rate limit:** 30 opens per user per hour.

`env pull` keeps writing names only.

## Consequences

- One mental model: a variable is sealed unless you said `--plain`, and every
  command takes the same keys.
- A bad rotation or config push is undoable for 30 days.
- Local development against a shared development environment needs no copied
  `.env` file, and production values never leave the platform.
- Revocation and app deletion must purge version history, or leaked values
  stay stored; slice 2 ships both together.
- `env exec` adds a plaintext-returning API. It is limited to non-production
  scopes, human MFA sessions and a dedicated permission, and is audited.

## Slices

1. Key shape: `SecretKeyPattern`, CHECK constraints and SQL functions move to
   mixed case; CLI validators follow.
2. Version history: tables, writes on replace/remove, sweeper, revocation and
   app purge, envelope rotation; `env rollback` restores secrets; `env rotate
   --undo`.
3. One model: kind conflict rule and migration, `env push --plain`, `--plain`
   credential refusal, unified `env list`, `environment env` verbs.
4. Guard restore uses version history; `app_config_guard_secrets` retired.
5. `env exec`: permission, route, secretd RPC, CLI, docs.
