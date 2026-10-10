-- A graph activation changes a GitOps candidate from held/snapshotting to
-- live. That reviewed state transition must not invalidate the same intent
-- whose lease authorizes the atomic promotion.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_secret_reference_baseline() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_app uuid; new_app uuid; old_scope text; new_scope text; src environment_git_sources%ROWTYPE;
BEGIN
 IF TG_OP='UPDATE' AND OLD.environment_workload_runtime IS NOT NULL AND NEW.environment_workload_runtime IS NOT NULL
  AND OLD.environment_workload_held AND OLD.status='snapshotting'
  AND NOT NEW.environment_workload_held AND NEW.status='live'
  AND environment_workload_activation_authorized(NEW) THEN
  RETURN NEW;
 END IF;
 IF TG_OP<>'INSERT' AND OLD.status='live' THEN old_app:=OLD.app_id; old_scope:=OLD.scope; END IF;
 IF TG_OP<>'DELETE' AND NEW.status='live' THEN new_app:=NEW.app_id; new_scope:=NEW.scope; END IF;
 IF old_app IS NULL AND new_app IS NULL THEN
  IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
 END IF;
 FOR src IN SELECT s.* FROM environment_git_sources s JOIN project_environments e ON e.id=s.environment_id
  JOIN apps a ON a.project_id=s.project_id AND a.account_id=s.account_id
  WHERE (a.id=old_app AND e.slug=old_scope) OR (a.id=new_app AND e.slug=new_scope)
  ORDER BY s.id FOR UPDATE OF s LOOP
  PERFORM 1 FROM apps a WHERE (a.id=old_app OR a.id=new_app) AND a.project_id=src.project_id ORDER BY a.id FOR UPDATE;
  UPDATE environment_git_sources SET intent_version=intent_version+1,updated_at=now() WHERE id=src.id;
  IF src.generation>0 THEN
   INSERT INTO environment_gitops_jobs(source_id,desired_generation,next_attempt_at) VALUES(src.id,src.generation,now())
   ON CONFLICT(source_id) DO UPDATE SET next_attempt_at=least(environment_gitops_jobs.next_attempt_at,excluded.next_attempt_at);
  END IF;
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;
-- +goose StatementEnd
-- +goose Down
-- Forward-only: reverting the guard could make GitOps activation stale itself.
SELECT 1;
