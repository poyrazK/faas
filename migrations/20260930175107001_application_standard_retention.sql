-- +goose Up
-- Keep publishing UUID provenance after a human account is erased. Definitions
-- carry no human names or email addresses; account erasure must not mutate an
-- immutable version or prevent deletion of the account.
ALTER TABLE application_standards DROP CONSTRAINT application_standards_created_by_fkey;
ALTER TABLE application_standard_versions DROP CONSTRAINT application_standard_versions_created_by_fkey;

ALTER TABLE application_standards DROP CONSTRAINT application_standards_org_id_fkey;
ALTER TABLE application_standards ADD CONSTRAINT application_standards_org_id_fkey
    FOREIGN KEY (org_id) REFERENCES orgs(id) ON DELETE CASCADE;
ALTER TABLE application_standard_versions DROP CONSTRAINT application_standard_versions_org_id_standard_id_fkey;
ALTER TABLE application_standard_versions ADD CONSTRAINT application_standard_versions_org_id_standard_id_fkey
    FOREIGN KEY (org_id, standard_id) REFERENCES application_standards(org_id, id) ON DELETE CASCADE;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_version_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- Organization erasure cascades through the parent foreign key. Direct
    -- customer/version deletion and every update remain prohibited.
    IF TG_OP = 'DELETE' AND pg_trigger_depth() > 1 THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'application standards and their versions are immutable' USING ERRCODE = '23514';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_identity_immutable
BEFORE UPDATE OR DELETE ON application_standards
FOR EACH ROW EXECUTE FUNCTION application_standard_version_immutable();

-- +goose Down
-- Actor erasure is irreversible: restoring creator foreign keys would reject
-- historical publisher UUIDs. Keep the corrected retention contract on rollback.
