-- Application-owned data. Apply with a migration role, never the worker role.
CREATE TABLE sdk_csv_payments (
    payment_id text PRIMARY KEY,
    import_id uuid UNIQUE NOT NULL,
    owner_id text NOT NULL,
    customer_id text NOT NULL,
    amount_cents integer NOT NULL CHECK (amount_cents > 0),
    currency text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    state text NOT NULL DEFAULT 'unpaid' CHECK (state IN ('unpaid','paid','cancelled')),
    input jsonb NOT NULL,
    applications integer NOT NULL DEFAULT 0 CHECK (applications >= 0)
);
