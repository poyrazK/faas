-- ADR-658: durable directory-owned ingress generations. Regional admission
-- and customer intent are unchanged. Export only follows committed bytes.
-- +goose Up
CREATE TABLE global_ingress_publication (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 issuer_id uuid NOT NULL CHECK(issuer_id <> '00000000-0000-0000-0000-000000000000'),
 generation bigint NOT NULL CHECK(generation BETWEEN 1 AND 9007199254740991),
 envelope bytea NOT NULL CHECK(octet_length(envelope) BETWEEN 1 AND 25165824)
);
-- +goose StatementBegin
CREATE FUNCTION guard_global_ingress_publication() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_global_directory_scope();
 IF TG_OP IN ('DELETE','TRUNCATE') THEN
  RAISE EXCEPTION 'ingress generation watermark cannot be deleted' USING ERRCODE='23514',CONSTRAINT='global_ingress_identity';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM global_runtime_quota_issuer WHERE issuer_id=NEW.issuer_id) OR
  (TG_OP='INSERT' AND NEW.generation<>1) OR
  (TG_OP='UPDATE' AND (NEW.issuer_id<>OLD.issuer_id OR NEW.generation<>OLD.generation+1)) THEN
  RAISE EXCEPTION 'ingress publisher identity or generation conflicts' USING ERRCODE='23514',CONSTRAINT='global_ingress_identity';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER global_ingress_publication_guard BEFORE INSERT OR UPDATE OR DELETE ON global_ingress_publication FOR EACH ROW EXECUTE FUNCTION guard_global_ingress_publication();
CREATE TRIGGER global_ingress_publication_truncate_guard BEFORE TRUNCATE ON global_ingress_publication FOR EACH STATEMENT EXECUTE FUNCTION guard_global_ingress_publication();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM global_ingress_publication) THEN
  RAISE EXCEPTION 'retain the published ingress watermark; rollback requires an explicit directory identity migration';
 END IF;
END; $$;
-- +goose StatementEnd
DROP TABLE global_ingress_publication;
DROP FUNCTION guard_global_ingress_publication();
