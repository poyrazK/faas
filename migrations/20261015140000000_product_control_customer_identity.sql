-- +goose Up
ALTER TABLE product_control_customers ADD COLUMN auth_organization_id uuid
    CHECK (auth_organization_id IS NULL OR auth_organization_id <> '00000000-0000-0000-0000-000000000000'::uuid);
CREATE UNIQUE INDEX product_control_customer_auth_organization
    ON product_control_customers(app_id, auth_organization_id) WHERE auth_organization_id IS NOT NULL;

-- +goose Down
DROP INDEX product_control_customer_auth_organization;
ALTER TABLE product_control_customers DROP COLUMN auth_organization_id;
