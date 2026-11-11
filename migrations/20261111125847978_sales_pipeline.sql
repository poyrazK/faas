-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE sales_leads (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
 owner_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (name=btrim(name) AND octet_length(name) BETWEEN 1 AND 120),
 email text NOT NULL CHECK (email=lower(btrim(email)) AND octet_length(email) BETWEEN 3 AND 254),
 company text NOT NULL DEFAULT '' CHECK (octet_length(company)<=200),
 stage text NOT NULL DEFAULT 'new' CHECK (stage IN ('new','contacted','qualified','demo','won','lost')),
 note text NOT NULL DEFAULT '' CHECK (octet_length(note)<=2000),
 version bigint NOT NULL DEFAULT 1 CHECK (version>0),
 created_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 updated_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
 updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at)),
 UNIQUE(owner_id,email)
);
CREATE INDEX sales_leads_owner_page ON sales_leads(owner_id,id);

CREATE TABLE sales_tasks (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
 lead_id uuid NOT NULL REFERENCES sales_leads(id) ON DELETE CASCADE,
 title text NOT NULL CHECK (title=btrim(title) AND octet_length(title) BETWEEN 1 AND 200),
 due_on date NOT NULL CHECK (isfinite(due_on) AND due_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31'),
 completed_at timestamptz CHECK (completed_at IS NULL OR isfinite(completed_at)),
 version bigint NOT NULL DEFAULT 1 CHECK (version>0),
 created_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 updated_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
 updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at))
);
CREATE INDEX sales_tasks_lead ON sales_tasks(lead_id);
CREATE INDEX sales_tasks_due_page ON sales_tasks(due_on,id);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS sales_tasks;
DROP TABLE IF EXISTS sales_leads;
