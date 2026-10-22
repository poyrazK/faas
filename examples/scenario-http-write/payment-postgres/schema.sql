CREATE TABLE IF NOT EXISTS public.gregale_write_ledgers (
 namespace text PRIMARY KEY CHECK (namespace ~ '^[0-9a-f]{32}$'),
 id uuid NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS public.gregale_write_charges (
 namespace text NOT NULL REFERENCES public.gregale_write_ledgers(namespace) ON DELETE CASCADE,
 operation_key text NOT NULL CHECK (operation_key ~ '^[a-zA-Z0-9_-]{1,128}$'),
 state text NOT NULL CHECK (state IN ('pending', 'confirmed')),
 fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
 PRIMARY KEY (namespace, operation_key)
);
