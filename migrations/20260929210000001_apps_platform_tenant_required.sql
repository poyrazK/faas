-- Platform operators may require verified end-customer identity on app traffic.
ALTER TABLE apps
    ADD COLUMN platform_tenant_required boolean NOT NULL DEFAULT false;
