-- +goose Up
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_plan_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_plan_check
  CHECK (plan IN ('free','hobby','pro','scale','unknown'));

-- +goose Down
-- Imported history must be assigned a verified plan before this migration
-- can be rolled back. The constraint change intentionally fails while any
-- unknown-plan invoice remains, rather than mislabeling it as Free.
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_plan_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_plan_check
  CHECK (plan IN ('free','hobby','pro','scale'));
