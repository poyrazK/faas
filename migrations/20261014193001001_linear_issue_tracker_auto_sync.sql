-- +goose Up
ALTER TABLE public.issue_tracker_auto_sync
 ADD COLUMN linear_open_state_id text NOT NULL DEFAULT '',
 ADD COLUMN linear_resolved_state_id text NOT NULL DEFAULT '',
 ADD CONSTRAINT issue_tracker_auto_sync_linear_mapping_check CHECK (
  (COALESCE(baseline->>'provider','')='github' AND linear_open_state_id='' AND linear_resolved_state_id='') OR
  (COALESCE(baseline->>'provider','')='linear' AND
   linear_open_state_id ~ '^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$' AND
   linear_resolved_state_id ~ '^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$' AND
   linear_open_state_id <> '00000000-0000-0000-0000-000000000000' AND
   linear_resolved_state_id <> '00000000-0000-0000-0000-000000000000' AND
   linear_open_state_id <> linear_resolved_state_id)
 );

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM issue_tracker_auto_sync WHERE COALESCE(baseline->>'provider','')='linear') THEN
  RAISE EXCEPTION 'Linear automatic sync policies must be preserved before rollback';
 END IF;
END $$;
ALTER TABLE public.issue_tracker_auto_sync DROP CONSTRAINT issue_tracker_auto_sync_linear_mapping_check,
 DROP COLUMN linear_open_state_id, DROP COLUMN linear_resolved_state_id;
-- +goose StatementEnd
