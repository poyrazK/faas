-- +goose Up
CREATE TABLE application_standard_log_destinations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 128),
    kind text NOT NULL CHECK (kind IN ('http_json', 'otlp')),
    target_url text NOT NULL CHECK (target_url LIKE 'https://%' AND octet_length(target_url) <= 2048),
    auth_header_sealed bytea NOT NULL DEFAULT '' CHECK (octet_length(auth_header_sealed) <= 8192),
    config_hash text NOT NULL CHECK (config_hash ~ '^[a-f0-9]{64}$'),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, id)
);
CREATE TABLE application_standard_publishers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 128),
    public_key_der bytea NOT NULL CHECK (octet_length(public_key_der) BETWEEN 64 AND 1024),
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, id)
);
CREATE INDEX application_standard_log_destinations_org_idx ON application_standard_log_destinations (org_id, id);
CREATE INDEX application_standard_publishers_org_idx ON application_standard_publishers (org_id, id);
CREATE TRIGGER application_standard_log_destination_immutable
BEFORE UPDATE OR DELETE ON application_standard_log_destinations
FOR EACH ROW EXECUTE FUNCTION application_standard_version_immutable();
CREATE TRIGGER application_standard_publisher_immutable
BEFORE UPDATE OR DELETE ON application_standard_publishers
FOR EACH ROW EXECUTE FUNCTION application_standard_version_immutable();

-- +goose Down
DROP TABLE application_standard_publishers;
DROP TABLE application_standard_log_destinations;
