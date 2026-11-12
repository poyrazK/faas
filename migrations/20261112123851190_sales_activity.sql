-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE sales_activities (
 id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
 lead_id uuid NOT NULL REFERENCES sales_leads(id) ON DELETE CASCADE,
 actor_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 kind text NOT NULL CHECK (kind IN ('call','email','demo','note')),
 summary text NOT NULL CHECK (summary=btrim(summary) AND octet_length(summary) BETWEEN 1 AND 1000),
 outcome text NOT NULL DEFAULT '' CHECK (octet_length(outcome)<=500),
 next_step text NOT NULL DEFAULT '' CHECK (octet_length(next_step)<=500),
 occurred_at timestamptz NOT NULL CHECK (isfinite(occurred_at) AND occurred_at>='2000-01-01T00:00:00Z'),
 recorded_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(recorded_at)),
 follow_up_title text NOT NULL DEFAULT '' CHECK (octet_length(follow_up_title)<=200),
 follow_up_on date CHECK (follow_up_on IS NULL OR (isfinite(follow_up_on) AND follow_up_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31')),
 task_id uuid REFERENCES sales_tasks(id) ON DELETE SET NULL,
 CHECK (occurred_at<=recorded_at),
 CHECK ((follow_up_title='')=(follow_up_on IS NULL))
);
CREATE INDEX sales_activities_page ON sales_activities(lead_id,occurred_at DESC,id DESC);
CREATE INDEX sales_activities_contact ON sales_activities(lead_id,occurred_at DESC) WHERE kind IN ('call','email','demo');
-- +goose Down
DROP TABLE sales_activities;
