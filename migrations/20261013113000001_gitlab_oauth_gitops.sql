-- +goose Up
CREATE TABLE gitlab_oauth_connections (
    account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    gitlab_user_id bigint NOT NULL CHECK (gitlab_user_id > 0),
    username text NOT NULL CHECK (username <> ''),
    credentials_sealed bytea NOT NULL CHECK (octet_length(credentials_sealed) > 0),
    expires_at timestamptz NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    connected_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE environment_git_sources ADD COLUMN provider text NOT NULL DEFAULT 'github';
ALTER TABLE environment_git_sources DROP CONSTRAINT environment_git_sources_installation_id_check;
ALTER TABLE environment_git_sources ADD CONSTRAINT environment_git_sources_installation_id_check
    CHECK ((provider = 'github' AND installation_id > 0) OR (provider = 'gitlab' AND installation_id = 0));
ALTER TABLE environment_git_sources DROP CONSTRAINT environment_git_sources_repository_check;
ALTER TABLE environment_git_sources ADD CONSTRAINT environment_git_sources_repository_check
    CHECK ((provider = 'github' AND repository ~ '^[^/[:space:]]+/[^/[:space:]]+$')
       OR (provider = 'gitlab' AND length(repository) <= 512
           AND repository ~ '^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)+$'
           AND repository !~ '(^|/)[.]{1,2}(/|$)'
           AND repository !~ '[.]git(/|$)'));
ALTER TABLE environment_git_sources ADD CONSTRAINT environment_git_sources_provider_check
    CHECK (provider IN ('github', 'gitlab'));
ALTER TABLE environment_git_sources ADD CONSTRAINT environment_git_sources_gitlab_policy_check
    CHECK (provider <> 'gitlab' OR (mode = 'report' AND approval_policy = 'manual' AND NOT prune));

-- +goose Down
-- Preserve OAuth connection and source identity until every bound source is retired.
SELECT 1;
