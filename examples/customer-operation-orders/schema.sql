-- Application-owned business schema; install explicitly in the application's database.
CREATE TABLE IF NOT EXISTS public.example_orders (
    id uuid PRIMARY KEY,
    platform_tenant_id uuid NOT NULL CHECK (platform_tenant_id <> '00000000-0000-0000-0000-000000000000'),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'fulfilled')),
    fulfillment_count integer NOT NULL DEFAULT 0,
    CHECK ((status = 'pending' AND fulfillment_count = 0)
        OR (status = 'fulfilled' AND fulfillment_count = 1))
);
