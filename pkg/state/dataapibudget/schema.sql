CREATE TABLE apps (id uuid PRIMARY KEY, account_id uuid NOT NULL, deleted_at timestamptz);
CREATE TABLE data_api_shared_budgets (
 app_id uuid NOT NULL,
 budget_key text NOT NULL,
 window_start timestamptz NOT NULL,
 window_end timestamptz NOT NULL,
 requests bigint NOT NULL,
 PRIMARY KEY(app_id,budget_key)
);
