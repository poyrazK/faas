-- +goose Up
CREATE TABLE deployment_public_build_urls (
 deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE CHECK (deployment_id <> '00000000-0000-0000-0000-000000000000'),
 urls jsonb NOT NULL CHECK (jsonb_typeof(urls)='object' AND octet_length(urls::text)<=24000)
);

-- Capture in the same transaction as every source deployment INSERT, including
-- alternate admission paths. Legacy rows deliberately have no build values.
-- +goose StatementBegin
CREATE FUNCTION capture_deployment_public_build_urls() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.kind IN ('tarball','github','dockerfile') AND NEW.scope='default' THEN
  INSERT INTO deployment_public_build_urls(deployment_id,urls)
  SELECT NEW.id,COALESCE(a.manifest->'public_build_urls','{}'::jsonb)
  FROM apps a WHERE a.id=NEW.app_id AND COALESCE(a.preview_of_slug,'')='';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER deployment_capture_public_build_urls AFTER INSERT ON deployments FOR EACH ROW EXECUTE FUNCTION capture_deployment_public_build_urls();

-- +goose Down
DROP TRIGGER deployment_capture_public_build_urls ON deployments;
DROP FUNCTION capture_deployment_public_build_urls();
DROP TABLE deployment_public_build_urls;
