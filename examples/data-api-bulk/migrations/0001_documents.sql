CREATE SCHEMA IF NOT EXISTS api;
CREATE TABLE api.documents (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  owner text NOT NULL,
  tenant text NOT NULL,
  body text NOT NULL,
  version integer NOT NULL DEFAULT 1 CHECK (version > 0)
);
ALTER TABLE api.documents ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_documents ON api.documents
  USING (owner = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND tenant = current_setting('request.jwt.claims', true)::jsonb ->> 'tenant')
  WITH CHECK (owner = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND tenant = current_setting('request.jwt.claims', true)::jsonb ->> 'tenant');
-- Every update advances the version, including ordinary REST updates. Clients
-- cannot reset it to reuse a stale version. The primary key remains immutable.
CREATE FUNCTION api.advance_document_version() RETURNS trigger
  LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  IF NEW.id <> OLD.id THEN RAISE EXCEPTION 'document identity is immutable'; END IF;
  NEW.version := OLD.version + 1;
  RETURN NEW;
END;
$$;
CREATE TRIGGER advance_version BEFORE UPDATE ON api.documents
  FOR EACH ROW EXECUTE FUNCTION api.advance_document_version();
REVOKE EXECUTE ON FUNCTION api.advance_document_version() FROM PUBLIC;
