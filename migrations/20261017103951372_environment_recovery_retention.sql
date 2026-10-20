-- migration-compat: contract
-- Recovery protection and retention guards require the coordinated GitOps rollout.
-- ADR-20261017190807: bounded artifact recovery protection from reviewed Git intent.
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $migration$
BEGIN
IF to_regclass(format('%I.environment_recovery_retained_releases',current_schema())) IS NOT NULL THEN RETURN; END IF;

-- Serving handoffs reset their current acknowledgement receipt. Capture the
-- original completion independently so a handoff cannot renew or erase history.
CREATE TABLE environment_recovery_serving_history (
 graph_id uuid PRIMARY KEY REFERENCES environment_workload_graphs(id) ON DELETE CASCADE CHECK(graph_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 source_id uuid NOT NULL REFERENCES environment_git_sources(id) ON DELETE CASCADE CHECK(source_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 release_set_id uuid NOT NULL REFERENCES project_release_sets(id) ON DELETE CASCADE CHECK(release_set_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 served_at timestamptz NOT NULL CHECK(isfinite(served_at))
);
CREATE INDEX environment_recovery_serving_history_source ON environment_recovery_serving_history(source_id,served_at DESC,release_set_id);
CREATE INDEX environment_recovery_serving_history_release ON environment_recovery_serving_history(release_set_id,source_id,served_at);
INSERT INTO environment_recovery_serving_history(graph_id,source_id,release_set_id,served_at)
 SELECT r.graph_id,r.source_id,r.release_set_id,r.served_at FROM environment_workload_serving_receipts r
 JOIN project_release_sets rs ON rs.id=r.release_set_id
 WHERE r.served_at IS NOT NULL;
CREATE FUNCTION environment_recovery_history_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'recovery serving history is immutable' USING ERRCODE='23514'; END IF;
  RETURN NEW;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM environment_workload_serving_receipts r WHERE r.graph_id=NEW.graph_id AND r.source_id=NEW.source_id AND r.release_set_id=NEW.release_set_id AND r.served_at=NEW.served_at) OR
 EXISTS(SELECT 1 FROM environment_recovery_serving_history h WHERE h.graph_id=NEW.graph_id AND (h.source_id,h.release_set_id) IS DISTINCT FROM (NEW.source_id,NEW.release_set_id)) THEN
  RAISE EXCEPTION 'recovery history requires matching completed serving evidence' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER environment_recovery_history_guard BEFORE INSERT OR UPDATE ON environment_recovery_serving_history FOR EACH ROW EXECUTE FUNCTION environment_recovery_history_guard();
CREATE FUNCTION environment_recovery_capture() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO environment_recovery_serving_history(graph_id,source_id,release_set_id,served_at)
 VALUES(NEW.graph_id,NEW.source_id,NEW.release_set_id,NEW.served_at) ON CONFLICT(graph_id) DO NOTHING;
 RETURN NEW;
END $$;
CREATE TRIGGER environment_recovery_capture AFTER INSERT OR UPDATE OF served_at ON environment_workload_serving_receipts
 FOR EACH ROW WHEN (NEW.served_at IS NOT NULL) EXECUTE FUNCTION environment_recovery_capture();

CREATE FUNCTION environment_recovery_policy_valid() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p jsonb:=NEW.definition->'recovery_retention';
BEGIN
 IF p IS NULL THEN RETURN NEW; END IF;
 IF jsonb_typeof(p->'releases')<>'number' OR jsonb_typeof(p->'seconds')<>'number' OR jsonb_typeof(p->'max_bytes')<>'number' OR
 jsonb_typeof(p)<>'object' OR NOT (p ?& ARRAY['releases','seconds','max_bytes']) OR
   (SELECT count(*) FROM jsonb_object_keys(p))<>3 OR
   coalesce(p->>'releases','') !~ '^[0-9]+$' OR coalesce(p->>'seconds','') !~ '^[0-9]+$' OR coalesce(p->>'max_bytes','') !~ '^[0-9]+$' THEN
  RAISE EXCEPTION 'invalid recovery retention policy' USING ERRCODE='23514';
 END IF;
 -- Mirrors api.EnvironmentRecovery* ceilings; exercised by the migration test.
 IF (p->>'releases')::numeric NOT BETWEEN 1 AND 5 OR (p->>'seconds')::numeric NOT BETWEEN 1 AND 604800 OR (p->>'max_bytes')::numeric NOT BETWEEN 1 AND 1073741824 THEN
  RAISE EXCEPTION 'recovery retention limit exceeded' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER environment_recovery_policy_valid BEFORE INSERT OR UPDATE OF definition ON environment_desired_revisions
 FOR EACH ROW EXECUTE FUNCTION environment_recovery_policy_valid();

CREATE VIEW environment_recovery_candidates AS
WITH policies AS (
 SELECT s.id source_id,s.account_id,s.project_id,s.environment_id,e.slug environment_slug,
  (v.definition->'recovery_retention'->>'releases')::integer max_releases,
  (v.definition->'recovery_retention'->>'seconds')::integer seconds,
  (v.definition->'recovery_retention'->>'max_bytes')::bigint max_bytes
 FROM environment_git_sources s JOIN environment_desired_revisions v ON v.id=s.approved_revision_id AND v.source_id=s.id
 JOIN project_environments e ON e.id=s.environment_id AND e.project_id=s.project_id
 WHERE NOT s.detached AND v.definition ? 'recovery_retention'
), history AS (
 SELECT p.*,r.release_set_id,max(r.served_at) served_at,min(g.id::text)::uuid graph_id,count(*) graph_count
 FROM policies p JOIN environment_workload_graphs g ON g.source_id=p.source_id AND g.environment_id=p.environment_id
 JOIN environment_recovery_serving_history r ON r.graph_id=g.id AND r.source_id=p.source_id
 JOIN project_release_sets rs ON rs.id=r.release_set_id AND rs.account_id=p.account_id AND rs.project_id=p.project_id AND rs.environment_slug=p.environment_slug
 WHERE r.served_at IS NOT NULL AND EXISTS(SELECT 1 FROM project_release_members rm WHERE rm.release_id=r.release_set_id)
 GROUP BY p.source_id,p.account_id,p.project_id,p.environment_id,p.environment_slug,p.max_releases,p.seconds,p.max_bytes,r.release_set_id
), inventory AS (
 -- A release stays protected while it is the newest completed serving release
 -- and for the policy age after the next release first completed serving.
 SELECT h.*,lag(h.served_at) OVER(PARTITION BY h.account_id,h.source_id ORDER BY h.served_at DESC,h.release_set_id DESC) superseded_at,
  coalesce((SELECT sum(k.bytes)::bigint FROM (
   SELECT key,max(bytes) bytes FROM (
    SELECT d.rootfs_key key,d.rootfs_bytes bytes FROM project_release_members rm JOIN deployments d ON d.id=rm.deployment_id WHERE rm.release_id=h.release_set_id
    UNION ALL SELECT l.storage_key,l.bytes FROM project_release_members rm JOIN deployment_sidecar_layers l ON l.deployment_id=rm.deployment_id WHERE rm.release_id=h.release_set_id
   ) parts GROUP BY key) k),0)::bigint bytes,
  h.graph_count=1 AND EXISTS(SELECT 1 FROM project_release_members rm WHERE rm.release_id=h.release_set_id)
  AND NOT EXISTS(SELECT 1 FROM project_release_members rm JOIN deployments d ON d.id=rm.deployment_id JOIN apps a ON a.id=d.app_id
   WHERE rm.release_id=h.release_set_id AND (a.account_id<>h.account_id OR a.project_id<>h.project_id OR d.app_id<>rm.app_id OR d.scope<>h.environment_slug OR coalesce(d.rootfs_key,'')='' OR coalesce(d.rootfs_bytes,0)<=0 OR d.deleted_at IS NOT NULL OR a.status='deleted'))
  AND NOT EXISTS(SELECT 1 FROM project_release_members rm JOIN deployment_sidecar_layers l ON l.deployment_id=rm.deployment_id WHERE rm.release_id=h.release_set_id AND (l.storage_key='' OR l.bytes<=0)) available
 FROM history h
), aged AS (
 SELECT i.*,i.superseded_at+make_interval(secs=>i.seconds) protected_until FROM inventory i
), ranked AS (
 -- account_id leads every partition so account-scoped guards push down.
 SELECT a.*,row_number() OVER(PARTITION BY account_id,source_id ORDER BY served_at DESC,release_set_id DESC) position,
 sum(bytes) OVER(PARTITION BY account_id,source_id ORDER BY served_at DESC,release_set_id DESC ROWS UNBOUNDED PRECEDING)::bigint charged_bytes
 FROM aged a WHERE a.protected_until IS NULL OR a.protected_until>clock_timestamp()
)
SELECT * FROM ranked;
CREATE VIEW environment_recovery_retained_releases AS
 SELECT * FROM environment_recovery_candidates WHERE position<=max_releases AND charged_bytes<=max_bytes;
CREATE VIEW environment_recovery_retained_deployments AS
 SELECT p.source_id,p.account_id,p.release_set_id,rm.deployment_id FROM environment_recovery_retained_releases p JOIN project_release_members rm ON rm.release_id=p.release_set_id
 UNION
 SELECT p.source_id,p.account_id,p.release_set_id,(d.environment_workload_runtime->'built_artifact'->>'deployment_id')::uuid
 FROM environment_recovery_retained_releases p JOIN project_release_members rm ON rm.release_id=p.release_set_id JOIN deployments d ON d.id=rm.deployment_id
 WHERE d.environment_workload_runtime ? 'built_artifact';
CREATE VIEW environment_recovery_retained_keys AS
 SELECT p.account_id,p.release_set_id,d.rootfs_key storage_key FROM environment_recovery_retained_releases p JOIN project_release_members rm ON rm.release_id=p.release_set_id JOIN deployments d ON d.id=rm.deployment_id
 UNION SELECT p.account_id,p.release_set_id,l.storage_key FROM environment_recovery_retained_releases p JOIN project_release_members rm ON rm.release_id=p.release_set_id JOIN deployment_sidecar_layers l ON l.deployment_id=rm.deployment_id;

-- Protection exists only under an approved policy of an attached source.
CREATE FUNCTION environment_recovery_account_has_policy(target uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM environment_git_sources s JOIN environment_desired_revisions v ON v.id=s.approved_revision_id AND v.source_id=s.id
  WHERE s.account_id=target AND NOT s.detached AND v.definition ? 'recovery_retention')
$$;
-- Accounts with a policy serialize approval, serving publication and metadata
-- writers exclusively on the account row. Every other account takes FOR KEY
-- SHARE: it never blocks foreign-key checks, ordinary account updates or other
-- writers, yet still orders against a first approval's exclusive lock, whose
-- policy the re-check below sees once granted. Returns whether to evaluate.
CREATE FUNCTION environment_recovery_lock_account(target uuid) RETURNS boolean LANGUAGE plpgsql AS $$
BEGIN
 IF target IS NULL THEN RETURN true; END IF;
 IF environment_recovery_account_has_policy(target) THEN
  PERFORM id FROM accounts WHERE id=target FOR UPDATE;
  RETURN true;
 END IF;
 PERFORM id FROM accounts WHERE id=target FOR KEY SHARE;
 RETURN environment_recovery_account_has_policy(target);
END $$;

-- Publication serializes reservations and metadata writers on the account. GC
-- serializes on each existing storage-key row and re-reads references after lock.
CREATE FUNCTION environment_recovery_fence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE account uuid; k text; state text; reserved numeric;
BEGIN
 IF TG_TABLE_NAME='environment_git_sources' THEN account:=NEW.account_id;
 ELSE SELECT account_id INTO account FROM environment_git_sources WHERE id=NEW.source_id; END IF;
 IF NOT environment_recovery_lock_account(account) THEN RETURN NEW; END IF;
 SELECT coalesce(sum((v.definition->'recovery_retention'->>'max_bytes')::numeric),0) INTO reserved
 FROM environment_git_sources s JOIN environment_desired_revisions v ON v.id=s.approved_revision_id
 WHERE s.account_id=account AND NOT s.detached AND v.definition ? 'recovery_retention';
 IF reserved>2147483648 THEN RAISE EXCEPTION 'account recovery retention reservation exceeded' USING ERRCODE='23514',CONSTRAINT='environment_recovery_budget',DETAIL=reserved::text; END IF;
 IF EXISTS(SELECT 1 FROM environment_recovery_retained_releases WHERE account_id=account AND NOT available) THEN
  RAISE EXCEPTION 'recovery artifact inventory unavailable' USING ERRCODE='23514',CONSTRAINT='environment_recovery_artifact_unavailable';
 END IF;
 FOR k IN SELECT DISTINCT storage_key FROM environment_recovery_retained_keys WHERE account_id=account ORDER BY storage_key LOOP
  INSERT INTO layer_artifact_retention(storage_key) VALUES(k) ON CONFLICT DO NOTHING;
  SELECT r.state INTO state FROM layer_artifact_retention r WHERE r.storage_key=k FOR UPDATE;
  IF state<>'retained' THEN RAISE EXCEPTION 'recovery artifact already retired' USING ERRCODE='23514',CONSTRAINT='environment_recovery_artifact_unavailable'; END IF;
 END LOOP;
 RETURN NEW;
END $$;
CREATE TRIGGER environment_recovery_fence AFTER INSERT OR UPDATE OF approved_revision_id,detached ON environment_git_sources
 FOR EACH ROW EXECUTE FUNCTION environment_recovery_fence();
CREATE TRIGGER environment_recovery_served_fence AFTER INSERT OR UPDATE OF served_at ON environment_workload_serving_receipts
 FOR EACH ROW WHEN (NEW.served_at IS NOT NULL) EXECUTE FUNCTION environment_recovery_fence();

-- Metadata cleanup must not erase the bytes or their historical proof. Trust
-- revocation and producer replacement remain independent and may block rollback.
CREATE FUNCTION environment_recovery_metadata_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE account uuid; identity uuid; blocked boolean:=false;
BEGIN
 IF TG_OP='INSERT' THEN
  IF TG_TABLE_NAME='project_release_members' THEN
   SELECT account_id INTO account FROM project_release_sets WHERE id=NEW.release_id;
   IF NOT environment_recovery_lock_account(account) THEN RETURN NEW; END IF;
   SELECT EXISTS(SELECT 1 FROM environment_recovery_retained_releases WHERE release_set_id=NEW.release_id AND (account IS NULL OR account_id=account)) INTO blocked;
  ELSE
   SELECT a.account_id INTO account FROM deployments d JOIN apps a ON a.id=d.app_id WHERE d.id=NEW.deployment_id;
   IF NOT environment_recovery_lock_account(account) THEN RETURN NEW; END IF;
   SELECT EXISTS(SELECT 1 FROM environment_recovery_retained_deployments WHERE deployment_id=NEW.deployment_id AND (account IS NULL OR account_id=account)) INTO blocked;
   IF blocked AND EXISTS(SELECT 1 FROM deployment_sidecar_layers l WHERE l.deployment_id=NEW.deployment_id AND l.sidecar_name=NEW.sidecar_name
    AND (l.storage_key,l.bytes,l.content_digest) IS NOT DISTINCT FROM (NEW.storage_key,NEW.bytes,NEW.content_digest)) THEN RETURN NEW; END IF;
  END IF;
  IF blocked THEN RAISE EXCEPTION 'release protected by recovery retention' USING ERRCODE='23514',CONSTRAINT='environment_recovery_retained'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='UPDATE' THEN
  IF TG_TABLE_NAME='environment_workload_serving_receipts' THEN
   IF (NEW.graph_id,NEW.source_id,NEW.release_set_id) IS NOT DISTINCT FROM (OLD.graph_id,OLD.source_id,OLD.release_set_id) THEN RETURN NEW; END IF;
  END IF;
  IF TG_TABLE_NAME='apps' THEN
   IF (NEW.status<>'deleted' OR OLD.status=NEW.status) AND (NEW.account_id,NEW.project_id) IS NOT DISTINCT FROM (OLD.account_id,OLD.project_id) THEN RETURN NEW; END IF;
  END IF;
  IF TG_TABLE_NAME='deployment_sidecar_layers' THEN
   IF (NEW.deployment_id,NEW.sidecar_name,NEW.storage_key,NEW.bytes,NEW.content_digest) IS NOT DISTINCT FROM (OLD.deployment_id,OLD.sidecar_name,OLD.storage_key,OLD.bytes,OLD.content_digest) THEN RETURN NEW; END IF;
  END IF;
  IF TG_TABLE_NAME='deployments' THEN
   IF (NEW.rootfs_path,NEW.rootfs_key,NEW.rootfs_bytes,NEW.deleted_at,NEW.environment_workload_runtime,NEW.app_id,NEW.scope,NEW.build_id,NEW.commit_sha,NEW.source_sha256,NEW.sidecars) IS NOT DISTINCT FROM
      (OLD.rootfs_path,OLD.rootfs_key,OLD.rootfs_bytes,OLD.deleted_at,OLD.environment_workload_runtime,OLD.app_id,OLD.scope,OLD.build_id,OLD.commit_sha,OLD.source_sha256,OLD.sidecars) THEN RETURN NEW; END IF;
  END IF;
  IF OLD IS NOT DISTINCT FROM NEW THEN RETURN NEW; END IF;
 END IF;
 IF TG_TABLE_NAME='accounts' THEN account:=OLD.id;
 ELSIF TG_TABLE_NAME='projects' THEN account:=OLD.account_id;
 ELSIF TG_TABLE_NAME='project_environments' THEN account:=OLD.account_id;
 ELSIF TG_TABLE_NAME='apps' THEN account:=OLD.account_id;
 ELSIF TG_TABLE_NAME='environment_git_sources' THEN account:=OLD.account_id;
 ELSIF TG_TABLE_NAME='project_release_sets' THEN account:=OLD.account_id;
 ELSIF TG_TABLE_NAME='environment_workload_graphs' THEN SELECT account_id INTO account FROM environment_git_sources WHERE id=OLD.source_id;
 ELSIF TG_TABLE_NAME IN ('environment_workload_serving_receipts','environment_recovery_serving_history') THEN SELECT account_id INTO account FROM environment_git_sources WHERE id=OLD.source_id;
 ELSIF TG_TABLE_NAME='environment_desired_revisions' THEN SELECT account_id INTO account FROM environment_git_sources WHERE id=OLD.source_id;
 ELSIF TG_TABLE_NAME='project_release_members' THEN SELECT account_id INTO account FROM project_release_sets WHERE id=OLD.release_id;
 ELSE
  IF TG_TABLE_NAME='deployments' THEN identity:=OLD.id; ELSE identity:=OLD.deployment_id; END IF;
  SELECT a.account_id INTO account FROM deployments d JOIN apps a ON a.id=d.app_id WHERE d.id=identity;
 END IF;
 IF TG_TABLE_NAME='environment_desired_revisions' THEN
  -- An approved revision gaining a policy in place must still be fenced.
  PERFORM id FROM accounts WHERE id=account FOR UPDATE;
 ELSIF NOT environment_recovery_lock_account(account) THEN
  IF TG_OP='UPDATE' THEN RETURN NEW; END IF;
  RETURN OLD;
 END IF;
 IF TG_TABLE_NAME='accounts' THEN
  SELECT EXISTS(SELECT 1 FROM environment_recovery_retained_releases WHERE account_id=OLD.id) INTO blocked;
 ELSIF TG_TABLE_NAME='projects' THEN
  SELECT EXISTS(SELECT 1 FROM environment_recovery_retained_releases WHERE project_id=OLD.id AND (account IS NULL OR account_id=account)) INTO blocked;
 ELSIF TG_TABLE_NAME='project_environments' THEN
  SELECT EXISTS(SELECT 1 FROM environment_recovery_retained_releases WHERE environment_id=OLD.id AND (account IS NULL OR account_id=account)) INTO blocked;
 ELSIF TG_TABLE_NAME='apps' THEN
  SELECT EXISTS(SELECT 1 FROM environment_recovery_retained_deployments p JOIN deployments d ON d.id=p.deployment_id WHERE d.app_id=OLD.id AND (account IS NULL OR p.account_id=account)) INTO blocked;
 ELSIF TG_TABLE_NAME='environment_git_sources' THEN
  SELECT EXISTS(SELECT 1 FROM environment_recovery_retained_releases WHERE source_id=OLD.id AND (account IS NULL OR account_id=account)) INTO blocked;
 ELSIF TG_TABLE_NAME='project_release_sets' THEN
  SELECT EXISTS(SELECT 1 FROM environment_recovery_retained_releases WHERE release_set_id=OLD.id AND (account IS NULL OR account_id=account)) INTO blocked;
 ELSIF TG_TABLE_NAME='environment_desired_revisions' THEN
  IF TG_OP='UPDATE' THEN
   SELECT EXISTS(SELECT 1 FROM environment_git_sources s WHERE s.approved_revision_id=OLD.id AND NOT s.detached AND (OLD.definition ? 'recovery_retention' OR NEW.definition ? 'recovery_retention')) INTO blocked;
  ELSE
   SELECT EXISTS(SELECT 1 FROM environment_git_sources s WHERE s.approved_revision_id=OLD.id AND NOT s.detached AND OLD.definition ? 'recovery_retention') INTO blocked;
  END IF;
  blocked:=blocked OR EXISTS(SELECT 1 FROM environment_recovery_retained_releases p JOIN environment_workload_graphs g ON g.id=p.graph_id WHERE g.revision_id=OLD.id AND (account IS NULL OR p.account_id=account));
 ELSIF TG_TABLE_NAME='project_release_members' THEN
  SELECT EXISTS(SELECT 1 FROM environment_recovery_retained_releases WHERE release_set_id=OLD.release_id AND (account IS NULL OR account_id=account)) INTO blocked;
 ELSIF TG_TABLE_NAME='environment_workload_graphs' THEN
  SELECT EXISTS(SELECT 1 FROM environment_recovery_retained_releases WHERE graph_id=OLD.id AND (account IS NULL OR account_id=account)) INTO blocked;
 ELSIF TG_TABLE_NAME IN ('environment_workload_serving_receipts','environment_recovery_serving_history') THEN
  SELECT EXISTS(SELECT 1 FROM environment_recovery_retained_releases WHERE graph_id=OLD.graph_id AND (account IS NULL OR account_id=account)) INTO blocked;
 ELSE SELECT EXISTS(SELECT 1 FROM environment_recovery_retained_deployments WHERE deployment_id=identity AND (account IS NULL OR account_id=account)) INTO blocked;
 END IF;
 IF blocked THEN RAISE EXCEPTION 'release protected by recovery retention' USING ERRCODE='23514',CONSTRAINT='environment_recovery_retained'; END IF;
 IF TG_OP='UPDATE' THEN RETURN NEW; END IF;
 RETURN OLD;
END $$;
CREATE TRIGGER environment_recovery_metadata_guard BEFORE DELETE OR UPDATE OF rootfs_path,rootfs_key,rootfs_bytes,deleted_at,environment_workload_runtime,app_id,scope,build_id,commit_sha,source_sha256,sidecars ON deployments
 FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
CREATE TRIGGER environment_recovery_metadata_guard BEFORE DELETE OR UPDATE ON environment_desired_revisions FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
CREATE TRIGGER environment_recovery_metadata_guard BEFORE DELETE ON source_build_rootfs FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
CREATE TRIGGER environment_recovery_metadata_guard BEFORE DELETE ON deployment_registry_rootfs FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
CREATE TRIGGER environment_recovery_metadata_guard BEFORE INSERT OR DELETE OR UPDATE ON deployment_sidecar_layers FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
CREATE TRIGGER environment_recovery_metadata_guard BEFORE INSERT OR DELETE OR UPDATE ON project_release_members FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
CREATE TRIGGER environment_recovery_metadata_guard BEFORE DELETE OR UPDATE OF account_id,project_id,environment_slug ON project_release_sets FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
CREATE TRIGGER environment_recovery_metadata_guard BEFORE DELETE ON environment_workload_graphs FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
CREATE TRIGGER environment_recovery_metadata_guard BEFORE DELETE ON environment_recovery_serving_history FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
CREATE TRIGGER environment_recovery_metadata_guard BEFORE DELETE OR UPDATE OF graph_id,source_id,release_set_id ON environment_workload_serving_receipts FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
CREATE TRIGGER environment_recovery_metadata_guard BEFORE DELETE OR UPDATE OF account_id,project_id,environment_id ON environment_git_sources FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
CREATE TRIGGER environment_recovery_metadata_guard BEFORE DELETE OR UPDATE OF status,account_id,project_id ON apps FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
CREATE TRIGGER environment_recovery_metadata_guard BEFORE DELETE ON accounts FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
CREATE TRIGGER environment_recovery_metadata_guard BEFORE DELETE ON projects FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
CREATE TRIGGER environment_recovery_metadata_guard BEFORE DELETE ON project_environments FOR EACH ROW EXECUTE FUNCTION environment_recovery_metadata_guard();
END
$migration$;
-- +goose StatementEnd
-- +goose Down
-- Explicit retained storage intent is forward-only.
SELECT 1;
