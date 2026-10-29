-- Data API file storage (ADR-20261021101510). Apply as the schema owner in a
-- migration. `gregale data-api storage sql` prints this with your schema.
-- Rows record the files the Data API stores; RLS policies on this table decide
-- who may upload (INSERT, UPDATE), download (SELECT) and remove (DELETE).

CREATE TABLE {{schema}}.storage_objects (
  bucket text NOT NULL CHECK (bucket ~ '^[a-z][a-z0-9_]{0,62}$'),
  name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 1024),
  owner text DEFAULT nullif(current_setting('request.jwt.claims', true), '')::jsonb ->> 'sub',
  size bigint NOT NULL CHECK (size >= 0),
  content_type text NOT NULL CHECK (octet_length(content_type) BETWEEN 1 AND 255),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  -- Where the upload's bytes live in the bucket. Every upload writes a new
  -- key; a row without one (for example a row you insert) names its bytes by
  -- name.
  object_key text CHECK (object_key ~ '^_gregale/objects/[0-9a-f]{32}$'),
  PRIMARY KEY (bucket, name)
);
ALTER TABLE {{schema}}.storage_objects ENABLE ROW LEVEL SECURITY;

-- Only the Data API's storage endpoints change rows: they add the
-- gateway-owned gregale_storage claim, which the gateway strips from every
-- token it receives. Updates keep the original owner and creation time.
CREATE FUNCTION {{schema}}.storage_objects_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
  IF coalesce(nullif(current_setting('request.jwt.claims', true), '')::jsonb ->> 'gregale_storage', '') <> 'write' THEN
    RAISE EXCEPTION 'storage objects change only through the Data API storage endpoints' USING ERRCODE = '42501';
  END IF;
  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  IF TG_OP = 'UPDATE' THEN
    NEW.owner := OLD.owner;
    NEW.created_at := OLD.created_at;
  END IF;
  NEW.updated_at := now();
  RETURN NEW;
END
$$;
CREATE TRIGGER storage_objects_guard BEFORE INSERT OR UPDATE OR DELETE ON {{schema}}.storage_objects
  FOR EACH ROW EXECUTE FUNCTION {{schema}}.storage_objects_guard();

-- Policies are yours. For example:
--
-- Signed-in users manage their own files in "uploads", up to 10 MiB each:
-- CREATE POLICY own_uploads ON {{schema}}.storage_objects
--   USING (bucket = 'uploads' AND owner = current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
--   WITH CHECK (bucket = 'uploads' AND owner = current_setting('request.jwt.claims', true)::jsonb ->> 'sub' AND size <= 10485760);
--
-- Everyone may download avatars (visitors also need
-- `gregale data-api configure NAME --public-read {{schema}}.storage_objects`):
-- CREATE POLICY read_avatars ON {{schema}}.storage_objects FOR SELECT USING (bucket = 'avatars');
