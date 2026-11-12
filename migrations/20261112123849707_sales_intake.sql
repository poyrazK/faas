-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Serialize deduplication and round-robin assignment across apid processes.
CREATE TABLE sales_intake_routing (
 id smallint PRIMARY KEY CHECK (id=1),
 last_owner_id uuid REFERENCES accounts(id) ON DELETE SET NULL
);
INSERT INTO sales_intake_routing(id) VALUES(1);

CREATE TABLE sales_inquiries (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
 name text NOT NULL CHECK (name=btrim(name) AND octet_length(name) BETWEEN 1 AND 120),
 email text NOT NULL CHECK (email=lower(btrim(email)) AND octet_length(email) BETWEEN 3 AND 254),
 company text NOT NULL DEFAULT '' CHECK (octet_length(company)<=200),
 intent text NOT NULL CHECK (intent IN ('contact','demo')),
 message text NOT NULL CHECK (octet_length(message) BETWEEN 1 AND 1500),
 latest_message text NOT NULL DEFAULT '' CHECK (octet_length(latest_message)<=1500),
 requested_code text NOT NULL DEFAULT '' CHECK (requested_code='' OR requested_code ~ '^[A-Z0-9]{6}$'),
 status text NOT NULL CHECK (status IN ('pending','assigned','closed')),
 reason text NOT NULL CHECK (reason IN ('','no_reps','code_unavailable','owner_conflict','multiple_leads','owner_unavailable','lead_unavailable')),
 lead_id uuid REFERENCES sales_leads(id) ON DELETE SET NULL,
 task_id uuid REFERENCES sales_tasks(id) ON DELETE SET NULL,
 repeat_count bigint NOT NULL DEFAULT 0 CHECK (repeat_count>=0),
 version bigint NOT NULL DEFAULT 1 CHECK (version>0),
 created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
 last_received_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(last_received_at)),
 updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at))
);
CREATE UNIQUE INDEX sales_inquiries_active_email ON sales_inquiries(email) WHERE status<>'closed';
CREATE INDEX sales_inquiries_lead ON sales_inquiries(lead_id);
CREATE INDEX sales_inquiries_page ON sales_inquiries(status,id);

-- Opaque, expiring anti-replay receipts hold no contact or network information.
CREATE TABLE sales_intake_receipts (
 nonce uuid PRIMARY KEY CHECK (nonce <> '00000000-0000-0000-0000-000000000000'),
 expires_at timestamptz NOT NULL CHECK (isfinite(expires_at))
);
CREATE INDEX sales_intake_receipts_expiry ON sales_intake_receipts(expires_at);

-- +goose Down
DROP TABLE sales_intake_receipts;
DROP TABLE sales_inquiries;
DROP TABLE sales_intake_routing;
