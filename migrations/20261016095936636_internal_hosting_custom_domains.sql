-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS hosting_domain_claims (
    hostname text PRIMARY KEY CHECK (length(hostname) BETWEEN 1 AND 64 AND hostname = lower(hostname)),
    domain_id uuid NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(hostname, domain_id)
);
CREATE TABLE IF NOT EXISTS hosting_domains (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    site_id uuid NOT NULL REFERENCES hosting_sites(id) ON DELETE RESTRICT,
    hostname text NOT NULL UNIQUE,
    provider_origin_id text NOT NULL CHECK (provider_origin_id ~ '^[0-9a-f]{64}$'),
    challenge text NOT NULL CHECK (challenge ~ '^[0-9a-f]{64}$'),
    state text NOT NULL DEFAULT 'pending_dns' CHECK (state IN ('pending_dns', 'pending_certificate', 'active', 'deleting')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision BETWEEN 1 AND 9007199254740991),
    generation bigint NOT NULL DEFAULT 1 CHECK (generation BETWEEN 1 AND 9007199254740991),
    published_generation bigint NOT NULL DEFAULT 0 CHECK (published_generation >= 0 AND published_generation <= generation),
    published_at timestamptz,
    details jsonb NOT NULL CHECK (jsonb_typeof(details) = 'object' AND octet_length(details::text) <= 16384),
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    attempts bigint NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error_code text NOT NULL DEFAULT '' CHECK (last_error_code IN ('', 'dns_pending', 'provider_unavailable', 'provider_conflict', 'publication_unavailable', 'origin_unavailable')),
    lease_token uuid,
    lease_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK ((published_generation > 0) = (published_at IS NOT NULL)),
    FOREIGN KEY (hostname, id) REFERENCES hosting_domain_claims(hostname, domain_id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS hosting_domains_due ON hosting_domains(next_attempt_at, id);
CREATE INDEX IF NOT EXISTS hosting_domains_site ON hosting_domains(site_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM hosting_domains) OR EXISTS (SELECT 1 FROM hosting_domain_claims) THEN
  RAISE EXCEPTION 'Preserve custom-domain reservations for the lifetime of the pilot; retire the isolated pilot before downgrade';
 END IF;
END $$;
DROP TABLE hosting_domains;
DROP TABLE hosting_domain_claims;
-- +goose StatementEnd
