-- migration-compat: expand
-- Each keyset batch commits independently; old workers populate new rows via
-- the root-account trigger installed in the preceding migration.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
DO $backfill$
DECLARE
 last_install bigint := 0;
 last_repo text := '';
 last_pr integer := 0;
BEGIN
 LOOP
  PERFORM set_config('lock_timeout','500ms',true);
  WITH batch AS (
   SELECT installation_id,repo_full_name,pr_number FROM pr_preview_sets
   WHERE (installation_id,repo_full_name,pr_number) > (last_install,last_repo,last_pr)
   ORDER BY installation_id,repo_full_name,pr_number LIMIT 1000
  ), moved AS (
   UPDATE pr_preview_sets s SET account_id=a.account_id
   FROM batch,apps a WHERE (s.installation_id,s.repo_full_name,s.pr_number)=(batch.installation_id,batch.repo_full_name,batch.pr_number)
   AND a.id=s.root_app_id AND s.account_id IS NULL
  )
  SELECT installation_id,repo_full_name,pr_number INTO last_install,last_repo,last_pr
  FROM batch ORDER BY installation_id DESC,repo_full_name DESC,pr_number DESC LIMIT 1;
  EXIT WHEN last_install IS NULL;
  COMMIT;
 END LOOP;
END
$backfill$;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
