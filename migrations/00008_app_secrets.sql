-- +goose Up
-- +goose StatementBegin

-- G2 customer secrets (spec §11, §17). Per-app (account_id, app_id, key) →
-- ciphertext blob, sealed at rest by the host age key (pkg/secretbox).
--
-- Why a CHECK on the key shape: secret values become Unix env-var names inside
-- the guest (`exec.Command` + `cmd.Env`), so the key MUST be a portable env
-- name — uppercase ASCII, digits, underscores, must start with a letter. The
-- CHECK and the apid input validator share pkg/api.SecretKeyPattern; keep them
-- in sync if the regex changes.
--
-- Why a UNIQUE (app_id, key): the apid PUT is an upsert (ON CONFLICT). Two
-- different keys cannot share a name on the same app; cross-app shadowing is
-- allowed because keys are scoped per-app, not per-account.
--
-- Why NO foreign key on app_id: the existing apps table uses uuid + on delete
-- cascade via a separate FK chain. We deliberately don't add a hard FK here
-- because (a) app deletion is async in pgstore.DeleteApp and (b) we want the
-- secrets rows to be torn down atomically when the app goes (a future
-- migration can add `references apps(id) on delete cascade` if the FK gap
-- becomes a problem; the cleanup is currently handled in pgstore.DeleteApp).

create table if not exists app_secrets (
  account_id  uuid        not null references accounts(id) on delete cascade,
  app_id      uuid        not null,
  key         text        not null,
  ciphertext  bytea       not null,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now(),
  primary key (app_id, key),
  constraint app_secrets_key_shape check (key ~ '^[A-Z][A-Z0-9_]*$' and length(key) <= 128)
);

create index if not exists app_secrets_account_idx
  on app_secrets (account_id);

create index if not exists app_secrets_app_idx
  on app_secrets (app_id);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
drop table if exists app_secrets;
-- +goose StatementEnd
