-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE sales_qualifications (
 lead_id uuid PRIMARY KEY REFERENCES sales_leads(id) ON DELETE CASCADE,
 use_case text NOT NULL DEFAULT '' CHECK (octet_length(use_case)<=1000),
 workload text NOT NULL DEFAULT '' CHECK (octet_length(workload)<=1000),
 decision_maker text NOT NULL DEFAULT '' CHECK (octet_length(decision_maker)<=200),
 buying_timeline text NOT NULL DEFAULT 'unknown' CHECK (buying_timeline IN ('unknown','exploring','30_days','90_days','later','date')),
 decision_on date CHECK (decision_on IS NULL OR (isfinite(decision_on) AND decision_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31')),
 priority text NOT NULL DEFAULT 'unknown' CHECK (priority IN ('unknown','low','medium','high')),
 product_fit text NOT NULL DEFAULT 'unknown' CHECK (product_fit IN ('unknown','strong','partial','poor')),
 blocker text NOT NULL DEFAULT '' CHECK (octet_length(blocker)<=1000),
 version bigint NOT NULL DEFAULT 1 CHECK (version>0),
 updated_by uuid REFERENCES accounts(id) ON DELETE SET NULL,
 updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at)),
 CHECK ((buying_timeline='date')=(decision_on IS NOT NULL))
);
CREATE TABLE sales_outcomes (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
 lead_id uuid NOT NULL REFERENCES sales_leads(id) ON DELETE CASCADE,
 lead_version bigint NOT NULL CHECK (lead_version>0),
 stage text NOT NULL CHECK (stage IN ('won','lost')),
 reason text NOT NULL CHECK (
  (stage='won' AND reason IN ('product_fit','capabilities','value','experience','referral','other')) OR
  (stage='lost' AND reason IN ('product_gap','budget','timing','competitor','no_response','no_need','other'))),
 note text NOT NULL DEFAULT '' CHECK (octet_length(note)<=1000),
 actor_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 owner_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 recorded_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(recorded_at)),
 CHECK (reason<>'other' OR btrim(note)<>''),
 UNIQUE(lead_id,lead_version)
);
CREATE INDEX sales_outcomes_latest ON sales_outcomes(lead_id,lead_version DESC);
-- +goose Down
DROP TABLE sales_outcomes;
DROP TABLE sales_qualifications;
