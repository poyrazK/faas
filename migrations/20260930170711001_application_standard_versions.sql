-- +goose Up
CREATE TABLE application_standards (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id uuid NOT NULL REFERENCES orgs(id),
    slug text NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$'),
    created_by uuid NOT NULL REFERENCES accounts(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug),
    UNIQUE (org_id, id)
);

CREATE TABLE application_standard_versions (
    org_id uuid NOT NULL,
    standard_id uuid NOT NULL,
    version bigint NOT NULL CHECK (version BETWEEN 1 AND 9007199254740991),
    definition jsonb NOT NULL CHECK (jsonb_typeof(definition) = 'object' AND definition <> '{}'::jsonb AND octet_length(definition::text) <= 131072),
    definition_hash text NOT NULL CHECK (definition_hash ~ '^[a-f0-9]{64}$'),
    description text NOT NULL DEFAULT '' CHECK (octet_length(description) <= 512),
    created_by uuid NOT NULL REFERENCES accounts(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (standard_id, version),
    UNIQUE (org_id, standard_id, version),
    FOREIGN KEY (org_id, standard_id) REFERENCES application_standards(org_id, id)
);

-- +goose StatementBegin
CREATE FUNCTION application_standard_version_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'application standard versions are immutable' USING ERRCODE = '23514';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_version_immutable
BEFORE UPDATE OR DELETE ON application_standard_versions
FOR EACH ROW EXECUTE FUNCTION application_standard_version_immutable();

-- +goose Down
DROP TABLE application_standard_versions;
DROP FUNCTION application_standard_version_immutable();
DROP TABLE application_standards;
