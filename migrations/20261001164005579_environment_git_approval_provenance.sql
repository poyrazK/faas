-- filename: 20261001164005579_environment_git_approval_provenance.sql
-- ADR-423: immutable reviewed-merge provenance; no trust backfill for legacy approvals.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE environment_git_sources DROP CONSTRAINT IF EXISTS environment_git_source_poll_error_code;
ALTER TABLE environment_git_sources ADD CONSTRAINT environment_git_source_poll_error_code CHECK (source_error_code IN (
  '', 'environment_git_source_unavailable', 'environment_git_definition_invalid',
  'environment_git_scope_mismatch', 'environment_git_repository_unavailable',
  'environment_git_approval_unavailable', 'environment_git_approval_not_qualified'));

CREATE TABLE IF NOT EXISTS environment_git_revision_approvals (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  source_id uuid NOT NULL REFERENCES environment_git_sources(id) ON DELETE CASCADE,
  revision_id uuid NOT NULL,
  approved_generation bigint NOT NULL CHECK (approved_generation > 0),
  definition_digest text NOT NULL CHECK (definition_digest ~ '^[a-f0-9]{64}$'),
  evidence jsonb NOT NULL CHECK (jsonb_typeof(evidence) = 'object' AND octet_length(evidence::text) <= 65536),
  poll_lease_token uuid NOT NULL,
  recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  FOREIGN KEY (source_id, revision_id) REFERENCES environment_desired_revisions(source_id, id) ON DELETE CASCADE,
  UNIQUE (source_id, revision_id, approved_generation)
);

CREATE OR REPLACE FUNCTION guard_environment_git_revision_approval() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  src environment_git_sources%ROWTYPE;
  rev environment_desired_revisions%ROWTYPE;
  proof jsonb;
  policy jsonb;
  checked timestamptz;
  policy_checked timestamptz;
  merged timestamptz;
  observed timestamptz := clock_timestamp();
  required_reviews integer;
  review jsonb;
  review_ids bigint[] := '{}';
  reviewer_ids bigint[] := '{}';
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF EXISTS (SELECT 1 FROM environment_git_sources WHERE id=OLD.source_id) THEN
      RAISE EXCEPTION 'Git approval provenance is immutable' USING ERRCODE='23514';
    END IF;
    RETURN OLD;
  END IF;
  IF TG_OP = 'UPDATE' THEN
    RAISE EXCEPTION 'Git approval provenance is immutable' USING ERRCODE='23514';
  END IF;
  SELECT * INTO STRICT src FROM environment_git_sources WHERE id=NEW.source_id FOR UPDATE;
  SELECT * INTO STRICT rev FROM environment_desired_revisions WHERE source_id=src.id AND id=NEW.revision_id;
  proof := NEW.evidence;
  policy := proof->'policy';
  checked := (proof->>'checked_at')::timestamptz;
  policy_checked := (policy->>'checked_at')::timestamptz;
  merged := (proof->>'merged_at')::timestamptz;
  required_reviews := (policy->>'required_review_count')::integer;
  IF src.approval_policy <> 'protected_branch' OR src.suspended
    OR proof->>'reviewed_definition_digest' IS DISTINCT FROM NEW.definition_digest
    OR NEW.definition_digest <> rev.definition_digest OR rev.commit_sha !~ '^[a-f0-9]{40}$'
    OR NEW.approved_generation NOT IN (src.generation, src.generation+1)
    OR proof->>'qualified' IS DISTINCT FROM 'true' OR coalesce(proof->>'reason','') <> ''
    OR proof->>'profile' IS DISTINCT FROM 'reviewed_merge/v1'
    OR policy->>'qualified' IS DISTINCT FROM 'true' OR coalesce(policy->>'reason','') <> ''
    OR policy->>'profile' IS DISTINCT FROM 'classic_reviewed_branch/v1'
    OR policy->>'installation_id' IS DISTINCT FROM src.installation_id::text
    OR policy->>'repository_id' IS DISTINCT FROM src.repository_id::text
    OR policy->>'repository' IS DISTINCT FROM src.repository
    OR src.source_ref NOT LIKE 'refs/heads/%'
    OR policy->>'branch' IS DISTINCT FROM substring(src.source_ref FROM 12)
    OR policy->>'commit_sha' IS DISTINCT FROM rev.commit_sha
    OR coalesce(policy->>'policy_digest','') !~ '^[a-f0-9]{64}$'
    OR coalesce(proof->>'head_sha','') !~ '^[a-f0-9]{40}$'
    OR coalesce((proof->>'pull_request_id')::bigint,0) <= 0
    OR coalesce((proof->>'pull_request_number')::bigint,0) <= 0
    OR coalesce((proof->>'author_id')::bigint,0) <= 0
    OR checked IS NULL OR policy_checked IS NULL OR merged IS NULL
    OR checked > observed OR policy_checked > checked OR merged > checked
    OR policy_checked < observed-interval '1 minute'
    OR coalesce(required_reviews,0) <= 0
    OR jsonb_typeof(proof->'reviews') IS DISTINCT FROM 'array'
    OR NOT EXISTS (SELECT 1 FROM environment_git_source_polls WHERE source_id=src.id
      AND lease_token=NEW.poll_lease_token AND lease_until > observed)
  THEN
    RAISE EXCEPTION 'Git approval evidence does not qualify for this source' USING ERRCODE='23514';
  END IF;
  FOR review IN SELECT value FROM jsonb_array_elements(proof->'reviews') LOOP
    IF coalesce((review->>'id')::bigint,0) <= 0 OR coalesce((review->>'reviewer_id')::bigint,0) <= 0
      OR (review->>'reviewer_id')::bigint = (proof->>'author_id')::bigint
      OR coalesce(review->>'reviewer','') = '' OR review->>'head_sha' IS DISTINCT FROM proof->>'head_sha'
      OR (review->>'submitted_at')::timestamptz IS NULL OR (review->>'submitted_at')::timestamptz >= merged
      OR (review->>'id')::bigint = ANY(review_ids) OR (review->>'reviewer_id')::bigint = ANY(reviewer_ids)
    THEN
      RAISE EXCEPTION 'Git approval review does not qualify' USING ERRCODE='23514';
    END IF;
    review_ids := array_append(review_ids,(review->>'id')::bigint);
    reviewer_ids := array_append(reviewer_ids,(review->>'reviewer_id')::bigint);
  END LOOP;
  IF cardinality(review_ids) < required_reviews THEN
    RAISE EXCEPTION 'Git approval reviews are insufficient' USING ERRCODE='23514';
  END IF;
  NEW.recorded_at := observed;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS environment_git_revision_approval_guard ON environment_git_revision_approvals;
CREATE TRIGGER environment_git_revision_approval_guard BEFORE INSERT OR UPDATE OR DELETE ON environment_git_revision_approvals
FOR EACH ROW EXECUTE FUNCTION guard_environment_git_revision_approval();

CREATE OR REPLACE FUNCTION guard_environment_protected_approval() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (OLD.approval_policy='protected_branch' OR NEW.approval_policy='protected_branch') AND
    (NEW.account_id,NEW.project_id,NEW.environment_id,NEW.repository_id,NEW.installation_id,NEW.repository,NEW.source_ref,NEW.manifest_path,NEW.approval_policy)
    IS DISTINCT FROM
    (OLD.account_id,OLD.project_id,OLD.environment_id,OLD.repository_id,OLD.installation_id,OLD.repository,OLD.source_ref,OLD.manifest_path,OLD.approval_policy)
  THEN
    RAISE EXCEPTION 'Protected Git source identity is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.approval_policy='protected_branch' AND NEW.generation < OLD.generation THEN
    RAISE EXCEPTION 'Protected Git generations cannot move backwards' USING ERRCODE='23514';
  END IF;
  IF NEW.approval_policy='protected_branch' AND NEW.approved_revision_id IS DISTINCT FROM OLD.approved_revision_id THEN
    IF NEW.generation <> OLD.generation+1 OR NOT EXISTS (
      SELECT 1 FROM environment_git_revision_approvals a
      WHERE a.source_id=NEW.id AND a.revision_id=NEW.approved_revision_id AND a.approved_generation=NEW.generation
        AND a.id::text=current_setting('faas.environment_git_approval_id',true)
        AND a.recorded_at >= clock_timestamp()-interval '1 minute')
    THEN
      RAISE EXCEPTION 'Protected Git approval requires verified merge provenance' USING ERRCODE='23514';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS environment_protected_approval_guard ON environment_git_sources;
CREATE TRIGGER environment_protected_approval_guard BEFORE UPDATE ON environment_git_sources
FOR EACH ROW EXECUTE FUNCTION guard_environment_protected_approval();
CREATE OR REPLACE FUNCTION guard_environment_protected_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM environment_git_sources WHERE id=OLD.source_id AND approval_policy='protected_branch') THEN
    IF TG_OP='DELETE' OR NEW IS DISTINCT FROM OLD THEN
      RAISE EXCEPTION 'Protected Git definition is immutable' USING ERRCODE='23514';
    END IF;
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS environment_protected_revision_guard ON environment_desired_revisions;
CREATE TRIGGER environment_protected_revision_guard BEFORE UPDATE OR DELETE ON environment_desired_revisions
FOR EACH ROW EXECUTE FUNCTION guard_environment_protected_revision();
-- +goose StatementEnd

-- +goose Down
-- Preserve accepted approvals and their evidence; old readers fail closed.
SELECT 1;
