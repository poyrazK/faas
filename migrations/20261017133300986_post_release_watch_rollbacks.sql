-- filename: 20261017133300986_post_release_watch_rollbacks.sql
--
-- ADR-1445: post-release watch. A release that regresses within thirty
-- minutes of completing rolls back with the trigger 'post_release' and is
-- marked last_auto_rollback_reason = 'post_release_regression'. Both checks
-- are extended in place: goose runs with allow-missing, so whatever they
-- admit now is kept.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $watch$
DECLARE previous_check text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO previous_check FROM pg_constraint
  WHERE conrelid = 'deployment_rollbacks'::regclass AND conname = 'deployment_rollbacks_trigger_check';
 IF previous_check IS NOT NULL AND position('post_release' IN previous_check) = 0 THEN
  ALTER TABLE deployment_rollbacks DROP CONSTRAINT deployment_rollbacks_trigger_check;
  EXECUTE format('ALTER TABLE deployment_rollbacks ADD CONSTRAINT deployment_rollbacks_trigger_check CHECK ((%s) OR trigger = ''post_release'')',
   regexp_replace(previous_check, '^CHECK \((.*)\)$', '\1'));
 END IF;
 SELECT pg_get_constraintdef(oid) INTO previous_check FROM pg_constraint
  WHERE conrelid = 'deployments'::regclass AND conname = 'deployments_last_auto_rollback_reason_check';
 IF previous_check IS NOT NULL AND position('post_release_regression' IN previous_check) = 0 THEN
  ALTER TABLE deployments DROP CONSTRAINT deployments_last_auto_rollback_reason_check;
  EXECUTE format('ALTER TABLE deployments ADD CONSTRAINT deployments_last_auto_rollback_reason_check CHECK ((%s) OR last_auto_rollback_reason = ''post_release_regression'')',
   regexp_replace(previous_check, '^CHECK \((.*)\)$', '\1'));
 END IF;
END $watch$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- Rows recorded by the watch keep their history under the nearest older
-- values: an alert-style rollback, and a threshold rollback.
UPDATE deployment_rollbacks SET trigger = 'alert' WHERE trigger = 'post_release';
UPDATE deployments SET last_auto_rollback_reason = 'threshold_exceeded' WHERE last_auto_rollback_reason = 'post_release_regression';
DO $watch$
DECLARE previous_check text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO previous_check FROM pg_constraint
  WHERE conrelid = 'deployment_rollbacks'::regclass AND conname = 'deployment_rollbacks_trigger_check';
 IF previous_check IS NOT NULL AND position('post_release' IN previous_check) > 0 THEN
  ALTER TABLE deployment_rollbacks DROP CONSTRAINT deployment_rollbacks_trigger_check;
  EXECUTE 'ALTER TABLE deployment_rollbacks ADD CONSTRAINT deployment_rollbacks_trigger_check ' ||
   regexp_replace(previous_check, ' OR \(trigger = ''post_release''::text\)', '');
 END IF;
 SELECT pg_get_constraintdef(oid) INTO previous_check FROM pg_constraint
  WHERE conrelid = 'deployments'::regclass AND conname = 'deployments_last_auto_rollback_reason_check';
 IF previous_check IS NOT NULL AND position('post_release_regression' IN previous_check) > 0 THEN
  ALTER TABLE deployments DROP CONSTRAINT deployments_last_auto_rollback_reason_check;
  EXECUTE 'ALTER TABLE deployments ADD CONSTRAINT deployments_last_auto_rollback_reason_check ' ||
   regexp_replace(previous_check, ' OR \(last_auto_rollback_reason = ''post_release_regression''::text\)', '');
 END IF;
END $watch$;
-- +goose StatementEnd
