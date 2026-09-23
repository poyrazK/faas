-- +goose Up
-- Repair app rows created while the personal-org attribution migration was
-- rolling out. New app writes persist org_id; this keeps existing activity
-- resources anchored to their owning personal org as well.
UPDATE apps a
   SET org_id = o.id
  FROM orgs o
 WHERE a.org_id IS NULL
   AND o.personal_org = true
   AND o.personal_owner_account_id = a.account_id;

-- +goose Down
-- Forward-only data repair: do not clear org ownership during rollback.
