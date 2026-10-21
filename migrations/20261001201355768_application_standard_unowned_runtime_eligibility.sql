-- filename: 20261001201355768_application_standard_unowned_runtime_eligibility.sql
-- adr: 393. Unowned compatibility retains account and app serving eligibility.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_runtime_is_unowned(application_id uuid) RETURNS boolean
    LANGUAGE plpgsql
    AS $$
DECLARE a apps%ROWTYPE; acct accounts%ROWTYPE;
BEGIN
 SELECT * INTO a FROM apps WHERE id=application_id FOR SHARE NOWAIT;
 IF NOT FOUND OR a.org_id IS NOT NULL OR a.status='deleted' THEN RETURN false; END IF;
 SELECT * INTO acct FROM accounts WHERE id=a.account_id FOR SHARE NOWAIT;
 IF NOT FOUND OR acct.status NOT IN ('active','past_due') OR acct.abuse_hold_at IS NOT NULL THEN RETURN false; END IF;
 -- Legacy residency creates no company enrollment, capture, grant or receipt.
 -- Retained company intent must never acquire this compatibility allowance.
 RETURN NOT EXISTS (SELECT 1 FROM app_application_standards WHERE app_id=a.id)
  AND NOT EXISTS (SELECT 1 FROM application_standard_assignments s WHERE s.active
    AND ((s.scope='application' AND s.scope_id=a.id) OR (s.scope='project' AND s.scope_id=a.project_id)));
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_runtime_is_unowned(application_id uuid) RETURNS boolean
    LANGUAGE plpgsql
    AS $$
DECLARE a apps%ROWTYPE;
BEGIN
 SELECT * INTO a FROM apps WHERE id=application_id FOR SHARE NOWAIT;
 IF NOT FOUND OR a.org_id IS NOT NULL THEN RETURN false; END IF;
 -- Legacy residency creates no company enrollment, capture, grant or receipt.
 -- Retained company intent must never acquire this compatibility allowance.
 RETURN NOT EXISTS (SELECT 1 FROM app_application_standards WHERE app_id=a.id)
  AND NOT EXISTS (SELECT 1 FROM application_standard_assignments s WHERE s.active
    AND ((s.scope='application' AND s.scope_id=a.id) OR (s.scope='project' AND s.scope_id=a.project_id)));
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
-- +goose StatementEnd
