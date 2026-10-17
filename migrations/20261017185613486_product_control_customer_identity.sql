-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $product_control_replay$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='product_control_customers' AND column_name='auth_organization_id') THEN
        ALTER TABLE product_control_customers ADD COLUMN auth_organization_id uuid
            CHECK (auth_organization_id IS NULL OR auth_organization_id <> '00000000-0000-0000-0000-000000000000'::uuid);
    END IF;
END;
$product_control_replay$;
-- +goose StatementEnd
CREATE UNIQUE INDEX IF NOT EXISTS product_control_customer_auth_organization
    ON product_control_customers(app_id, auth_organization_id) WHERE auth_organization_id IS NOT NULL;

-- +goose Down
DROP INDEX product_control_customer_auth_organization;
ALTER TABLE product_control_customers DROP COLUMN auth_organization_id;
