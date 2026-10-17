-- filename: 20261017091338430_config_revisions_and_guards.sql
--
-- ADR-1451: guarded, reversible config changes. Customer writes to an app
-- scope's variables or secrets overwrite app_envs/app_secrets in place, so a
-- bad value could neither be noticed nor undone. Each write now records a
-- complete revision of the scope (variable values, secret versions and value
-- fingerprints; never ciphertext) and opens or joins a config guard. apid's
-- guard worker judges the first new-config traffic and restores the guard's
-- base revision when it fails. app_config_guard_secrets keeps the sealed row
-- each guarded secret write replaced, only while its guard is watching.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS app_config_guards (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 scope text NOT NULL CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'),
 base_revision bigint NOT NULL CHECK (base_revision >= 1),
 head_revision bigint NOT NULL,
 status text NOT NULL CHECK (status IN ('watching','clean','restored','regressed','no_traffic','superseded')),
 restorable boolean NOT NULL DEFAULT true,
 opened_at timestamptz NOT NULL,
 last_change_at timestamptz NOT NULL,
 window_ends_at timestamptz,
 closed_at timestamptz,
 decision jsonb CHECK (decision IS NULL OR jsonb_typeof(decision) = 'object'),
 CHECK (head_revision >= base_revision),
 CHECK (last_change_at >= opened_at),
 CHECK ((status = 'watching') = (closed_at IS NULL)),
 CHECK (status = 'watching' OR decision IS NOT NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS app_config_guards_one_watching
 ON app_config_guards(app_id, scope) WHERE status = 'watching';
CREATE INDEX IF NOT EXISTS app_config_guards_watching
 ON app_config_guards(last_change_at, id) WHERE status = 'watching';
CREATE INDEX IF NOT EXISTS app_config_guards_app
 ON app_config_guards(app_id, scope, opened_at DESC);

CREATE TABLE IF NOT EXISTS app_config_revisions (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 scope text NOT NULL CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'),
 revision bigint NOT NULL CHECK (revision >= 1),
 source text NOT NULL CHECK (source IN ('baseline','env.set','env.delete','secret.set','secret.delete','secret.rotate','config.rollback','config.guard_restore')),
 config_key text NOT NULL DEFAULT '' CHECK (config_key = '' OR (config_key ~ '^[A-Z][A-Z0-9_]*$' AND length(config_key) <= 128)),
 actor text NOT NULL DEFAULT '' CHECK (octet_length(actor) <= 256),
 variables jsonb NOT NULL CHECK (jsonb_typeof(variables) = 'object'),
 secrets jsonb NOT NULL CHECK (jsonb_typeof(secrets) = 'object'),
 guard_id uuid REFERENCES app_config_guards(id) ON DELETE SET NULL,
 restored_revision bigint CHECK (restored_revision IS NULL OR restored_revision >= 1),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (app_id, scope, revision),
 CHECK ((source IN ('baseline','config.rollback','config.guard_restore')) = (config_key = '')),
 CHECK ((source IN ('config.rollback','config.guard_restore')) = (restored_revision IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS app_config_revisions_guard
 ON app_config_revisions(guard_id) WHERE guard_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS app_config_guard_secrets (
 guard_id uuid NOT NULL REFERENCES app_config_guards(id) ON DELETE CASCADE,
 config_key text NOT NULL CHECK (config_key ~ '^[A-Z][A-Z0-9_]*$' AND length(config_key) <= 128),
 existed boolean NOT NULL,
 ciphertext bytea,
 kid text,
 value_hash text,
 secret_class text CHECK (secret_class IS NULL OR secret_class IN ('persistent','ephemeral')),
 PRIMARY KEY (guard_id, config_key),
 CHECK (existed = (ciphertext IS NOT NULL)),
 CHECK (existed = (secret_class IS NOT NULL))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_config_guard_secrets;
DROP TABLE IF EXISTS app_config_revisions;
DROP TABLE IF EXISTS app_config_guards;
-- +goose StatementEnd
