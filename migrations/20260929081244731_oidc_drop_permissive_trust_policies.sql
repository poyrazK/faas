-- +goose Up
-- +goose StatementBegin

-- Before 2026-09-05 the first OIDC token exchange auto-created a trust
-- policy with an empty subject_pattern and an empty audience: any token
-- from the issuer (any GitHub repository, any requested audience) passed
-- it, and account resolution treated an empty pattern as "matches every
-- subject". Such a row let an unrelated repository's workflow mint a
-- deploy bearer for that account. Nothing binds through an empty pattern
-- any more; delete the rows so the next exchange from the account's own
-- repository re-creates a policy pinned to its exact subject and audience.
delete from oidc_trust_policies
 where coalesce(subject_pattern, '') = ''
    or coalesce(cardinality(audience), 0) = 0;

-- +goose StatementEnd

-- +goose Down
-- The deleted rows granted any subject; they are not restored.
select 1;
