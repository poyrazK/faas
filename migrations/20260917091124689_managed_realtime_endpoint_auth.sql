-- Per-endpoint client authentication policy for managed realtime (Realtime v2).
-- Public OIDC metadata is durable; bearer secrets remain age-sealed.

-- +goose Up
alter table managed_realtime_endpoints
    add column if not exists auth_mode text not null default 'none',
    add column if not exists auth_issuer text not null default '',
    add column if not exists auth_jwks_url text not null default '',
    add column if not exists auth_audience text[] not null default '{}'::text[],
    add column if not exists auth_algorithms text[] not null default '{}'::text[],
    add column if not exists auth_required_claims jsonb not null default '{}'::jsonb;

update managed_realtime_endpoints
   set auth_mode = 'static_bearer'
 where auth_mode = 'none' and octet_length(auth_token_sealed) > 0;

alter table managed_realtime_endpoints
    drop constraint if exists managed_realtime_endpoint_auth_chk;

alter table managed_realtime_endpoints
    add constraint managed_realtime_endpoint_auth_chk
    check (auth_mode in ('none', 'static_bearer', 'oidc_jwt')
       and cardinality(auth_audience) <= 16
       and cardinality(auth_algorithms) between 0 and 16
       and jsonb_typeof(auth_required_claims) = 'object');

-- +goose Down
alter table managed_realtime_endpoints
    drop constraint if exists managed_realtime_endpoint_auth_chk;
alter table managed_realtime_endpoints
    drop column if exists auth_mode,
    drop column if exists auth_issuer,
    drop column if exists auth_jwks_url,
    drop column if exists auth_audience,
    drop column if exists auth_algorithms,
    drop column if exists auth_required_claims;
