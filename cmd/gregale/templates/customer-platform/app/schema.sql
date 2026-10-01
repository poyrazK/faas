CREATE TABLE IF NOT EXISTS public.customer_documents (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    content text NOT NULL CHECK (octet_length(content) <= 16384),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
ALTER TABLE public.customer_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.customer_documents FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS customer_documents_tenant ON public.customer_documents;
CREATE POLICY customer_documents_tenant ON public.customer_documents
    USING (tenant_id = nullif(current_setting('gregale.platform_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('gregale.platform_tenant_id', true), '')::uuid);
