-- filename: 20261003174600000_environment_runtime_receipt_secret_audience.sql
-- ADR-459, ADR-462: serving receipts attest only credentials eligible for serving delivery.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_runtime_inputs_fresh(target_app uuid,target_scope text,boundary timestamptz,
 observed_variables jsonb,observed_secrets jsonb,observed_all_secrets boolean,observed_secret_refs jsonb)
RETURNS boolean LANGUAGE sql STABLE AS $$
 WITH managed AS (SELECT environment_scoped_secret_refs(target_app,target_scope) AS refs),
 suppressed AS (SELECT environment_scoped_secret_suppressions(target_app,target_scope) AS keys),
 eligible AS (
  SELECT s.key,s.scope,s.delivery_version FROM app_secrets s
  WHERE s.app_id=target_app AND s.scope=target_scope AND
   (s.managed_postgres_binding_id IS NULL OR EXISTS (
    SELECT 1 FROM managed_postgres_bindings b WHERE b.id=s.managed_postgres_binding_id
     AND b.account_id=s.account_id AND b.app_id=s.app_id AND b.scope=s.scope
     AND b.environment_key=s.key AND b.access IN ('read_write','read_only')))
 ),
 baseline AS (SELECT coalesce(jsonb_object_agg(s.key,'secret:'||s.key),'{}'::jsonb) AS refs,
  coalesce(jsonb_object_agg(s.scope||'/'||s.key,s.delivery_version),'{}'::jsonb) AS versions FROM eligible s)
 SELECT environment_runtime_base_inputs_fresh(target_app,target_scope,boundary,observed_variables,observed_secrets,false)
  AND managed.refs <@ observed_secret_refs
  AND NOT observed_secret_refs ?| suppressed.keys
  AND NOT EXISTS (SELECT 1 FROM jsonb_each_text(observed_secrets) v WHERE NOT EXISTS (
   SELECT 1 FROM eligible s WHERE v.key=s.scope||'/'||s.key AND v.value=s.delivery_version::text))
  AND (cardinality(suppressed.keys)=0 OR observed_secrets=coalesce((SELECT jsonb_object_agg(s.scope||'/'||s.key,s.delivery_version)
   FROM eligible s WHERE EXISTS (
    SELECT 1 FROM jsonb_each_text(observed_secret_refs) r WHERE r.value='secret:'||s.key)),'{}'::jsonb))
  AND NOT EXISTS(SELECT 1 FROM jsonb_each(managed.refs) r WHERE observed_variables ? r.key)
  AND NOT EXISTS (SELECT 1 FROM jsonb_each_text(observed_secret_refs) r WHERE
   NOT EXISTS (SELECT 1 FROM eligible s WHERE r.value='secret:'||s.key
    AND observed_secrets->>(target_scope||'/'||s.key)=s.delivery_version::text))
  AND (NOT observed_all_secrets OR
   CASE WHEN observed_secret_refs='{}'::jsonb AND managed.refs='{}'::jsonb AND cardinality(suppressed.keys)=0
    THEN observed_secrets=baseline.versions
    ELSE observed_secret_refs=((baseline.refs - suppressed.keys)||managed.refs) END)
 FROM managed,baseline,suppressed;
$$;
-- +goose StatementEnd
-- +goose Down
-- Rollback must preserve the serving/release credential boundary.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
