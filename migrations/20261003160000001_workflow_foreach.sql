-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION workflow_foreach_item_name(parent text, item_index integer)
RETURNS text LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
 SELECT '_foreach.' || rtrim(translate(replace(encode(convert_to(parent,'UTF8'),'base64'), E'\n',''), '+/', '-_'),'=') || '.' || item_index::text;
$$;

CREATE OR REPLACE FUNCTION workflow_step_definition(snapshot jsonb, name text, parent text, item_index integer)
RETURNS jsonb LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT CASE WHEN parent IS NULL THEN
  (SELECT def FROM jsonb_array_elements(snapshot->'steps') def WHERE def->>'name'=name LIMIT 1)
 WHEN name=workflow_foreach_item_name(parent,item_index) THEN
  (SELECT def->'for_each'->'action' FROM jsonb_array_elements(snapshot->'steps') def
   WHERE def->>'name'=parent AND jsonb_typeof(def->'for_each'->'action')='object' LIMIT 1)
 END;
$$;
-- +goose StatementEnd
ALTER TABLE workflow_steps
 ADD COLUMN IF NOT EXISTS foreach_parent text,
 ADD COLUMN IF NOT EXISTS foreach_index integer,
 ADD COLUMN IF NOT EXISTS foreach_count integer;
ALTER TABLE workflow_steps
 DROP CONSTRAINT IF EXISTS workflow_steps_foreach_parent_check,
 DROP CONSTRAINT IF EXISTS workflow_steps_foreach_index_check,
 DROP CONSTRAINT IF EXISTS workflow_steps_foreach_count_check,
 DROP CONSTRAINT IF EXISTS workflow_foreach_identity,
 DROP CONSTRAINT IF EXISTS workflow_foreach_parent,
 DROP CONSTRAINT IF EXISTS workflow_foreach_position,
 ADD CONSTRAINT workflow_steps_foreach_parent_check CHECK (foreach_parent IS NULL OR (octet_length(foreach_parent) BETWEEN 1 AND 64)),
 ADD CONSTRAINT workflow_steps_foreach_index_check CHECK (foreach_index IS NULL OR foreach_index BETWEEN 0 AND 127),
 ADD CONSTRAINT workflow_steps_foreach_count_check CHECK (foreach_count IS NULL OR foreach_count BETWEEN 0 AND 128),
 ADD CONSTRAINT workflow_foreach_identity CHECK ((foreach_parent IS NULL) = (foreach_index IS NULL)
   AND (foreach_parent IS NULL OR (foreach_count IS NULL AND step_name=workflow_foreach_item_name(foreach_parent,foreach_index)))),
 ADD CONSTRAINT workflow_foreach_parent FOREIGN KEY (run_id,foreach_parent) REFERENCES workflow_steps(run_id,step_name) ON DELETE CASCADE,
 ADD CONSTRAINT workflow_foreach_position UNIQUE(run_id,foreach_parent,foreach_index);

-- +goose Down
-- Pause admissions, drain/cancel runs and export item history before downgrade.
-- Older schedulers cannot interpret internal item rows; their attempts cascade.
DELETE FROM workflow_steps WHERE foreach_parent IS NOT NULL;
ALTER TABLE workflow_steps DROP CONSTRAINT workflow_foreach_position,
 DROP CONSTRAINT workflow_foreach_parent, DROP CONSTRAINT workflow_foreach_identity,
 DROP COLUMN foreach_count, DROP COLUMN foreach_index, DROP COLUMN foreach_parent;
DROP FUNCTION workflow_step_definition(jsonb,text,text,integer);
DROP FUNCTION workflow_foreach_item_name(text,integer);
