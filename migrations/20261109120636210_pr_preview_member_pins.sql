-- filename: 20261109120636210_pr_preview_member_pins.sql
-- migration-compat: expand
-- ADR-20261108150000: a PR push rebuilds only the preview services whose
-- files changed. A service it skips keeps serving the commit it was built
-- from, recorded here by app id. The pins hold only while member_pins_head
-- is the set's commit_sha: the previous release moves commit_sha without
-- knowing the pins, which leaves them inert. Both columns are nullable with
-- no default (catalog-only); the CHECKs are added NOT VALID and validated
-- separately. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE pr_preview_sets ADD COLUMN IF NOT EXISTS member_pins jsonb;
ALTER TABLE pr_preview_sets ADD COLUMN IF NOT EXISTS member_pins_head text;

-- +goose StatementBegin
DO $pins$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'pr_preview_sets'::regclass AND conname = 'pr_preview_sets_member_pins_chk') THEN
    ALTER TABLE pr_preview_sets ADD CONSTRAINT pr_preview_sets_member_pins_chk
      CHECK (member_pins IS NULL OR jsonb_typeof(member_pins) = 'object') NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'pr_preview_sets'::regclass AND conname = 'pr_preview_sets_member_pins_head_chk') THEN
    ALTER TABLE pr_preview_sets ADD CONSTRAINT pr_preview_sets_member_pins_head_chk
      CHECK (member_pins_head IS NULL OR member_pins_head ~ '^[0-9a-f]{7,64}$') NOT VALID;
  END IF;
END
$pins$;
-- +goose StatementEnd

ALTER TABLE pr_preview_sets VALIDATE CONSTRAINT pr_preview_sets_member_pins_chk;
ALTER TABLE pr_preview_sets VALIDATE CONSTRAINT pr_preview_sets_member_pins_head_chk;

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE pr_preview_sets DROP CONSTRAINT IF EXISTS pr_preview_sets_member_pins_head_chk;
ALTER TABLE pr_preview_sets DROP CONSTRAINT IF EXISTS pr_preview_sets_member_pins_chk;
ALTER TABLE pr_preview_sets DROP COLUMN IF EXISTS member_pins_head;
ALTER TABLE pr_preview_sets DROP COLUMN IF EXISTS member_pins;
