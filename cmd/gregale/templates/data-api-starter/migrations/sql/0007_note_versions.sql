ALTER TABLE api.notes ADD COLUMN version integer NOT NULL DEFAULT 1 CHECK (version > 0);

CREATE FUNCTION api.manage_note_version() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.version IS DISTINCT FROM 1 THEN
      RAISE EXCEPTION 'note version is managed by the database' USING ERRCODE = '42501';
    END IF;
  ELSE
    IF NEW.version IS DISTINCT FROM OLD.version THEN
      RAISE EXCEPTION 'note version is managed by the database' USING ERRCODE = '42501';
    END IF;
    NEW.version := OLD.version + 1;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER manage_note_version BEFORE INSERT OR UPDATE ON api.notes
FOR EACH ROW EXECUTE FUNCTION api.manage_note_version();
