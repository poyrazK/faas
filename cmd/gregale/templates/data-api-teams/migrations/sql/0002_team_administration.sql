-- Trusted identity-provider IDs are provisioned by the migration owner.
CREATE TABLE api.team_users (subject text PRIMARY KEY CHECK (subject <> '' AND length(subject) <= 256));
INSERT INTO api.team_users(subject) SELECT DISTINCT subject FROM api.team_members;
ALTER TABLE api.team_users ENABLE ROW LEVEL SECURITY;
CREATE POLICY known_user_reads ON api.team_users FOR SELECT USING (
  subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
  OR NULLIF(current_setting('gregale.team_member_write', true), '') IS NOT NULL
);
ALTER TABLE api.team_members ADD COLUMN revision uuid NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE api.team_members ADD CONSTRAINT registered_team_user FOREIGN KEY (subject) REFERENCES api.team_users(subject);
CREATE POLICY scoped_member_reads ON api.team_members FOR SELECT USING (
  tenant_id = current_setting('gregale.team_member_read', true)
);
CREATE POLICY scoped_member_creates ON api.team_members FOR INSERT WITH CHECK (
  tenant_id = current_setting('gregale.team_member_write', true)
);
CREATE POLICY scoped_member_updates ON api.team_members FOR UPDATE USING (
  tenant_id = current_setting('gregale.team_member_write', true)
) WITH CHECK (tenant_id = current_setting('gregale.team_member_write', true));
CREATE POLICY scoped_member_deletes ON api.team_members FOR DELETE USING (
  tenant_id = current_setting('gregale.team_member_write', true)
);

-- Serializes owner loss even for direct owner SQL, and prevents revision ABA.
CREATE FUNCTION api.guard_team_membership() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  IF current_setting('transaction_isolation') <> 'read committed' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'membership_read_committed_required';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || OLD.tenant_id, 0));
  IF TG_OP = 'UPDATE' THEN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.subject IS DISTINCT FROM OLD.subject THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE = 'membership_key_immutable';
    END IF;
    NEW.revision := gen_random_uuid();
  END IF;
  IF OLD.role = 'owner' AND (TG_OP = 'DELETE' OR NEW.role <> 'owner')
    AND NOT EXISTS (SELECT 1 FROM api.team_members m WHERE m.tenant_id = OLD.tenant_id AND m.subject <> OLD.subject AND m.role = 'owner') THEN
    RAISE SQLSTATE 'PT409' USING MESSAGE = 'last_team_owner';
  END IF;
  IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.guard_team_membership() FROM PUBLIC;
CREATE TRIGGER guard_team_membership BEFORE UPDATE OR DELETE ON api.team_members
FOR EACH ROW EXECUTE FUNCTION api.guard_team_membership();

CREATE TYPE api.team_change_action AS ENUM ('add', 'change', 'remove', 'transfer');
CREATE TABLE api.team_membership_events (
  event_id uuid NOT NULL DEFAULT gen_random_uuid(),
  tenant_id text NOT NULL,
  actor text NOT NULL,
  request_key uuid NOT NULL,
  action api.team_change_action NOT NULL,
  target_subject text NOT NULL,
  before_role api.team_role,
  after_role api.team_role,
  before_revision uuid,
  after_revision uuid,
  actor_before_revision uuid NOT NULL,
  actor_after_revision uuid,
  actor_after_role api.team_role,
  occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  payload jsonb NOT NULL,
  PRIMARY KEY (tenant_id, actor, request_key),
  UNIQUE (tenant_id, event_id)
);
CREATE INDEX team_membership_history ON api.team_membership_events(tenant_id, occurred_at DESC, event_id DESC);
ALTER TABLE api.team_membership_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY owner_event_reads ON api.team_membership_events FOR SELECT USING (
  EXISTS (SELECT 1 FROM api.team_members m WHERE m.tenant_id = team_membership_events.tenant_id
    AND m.subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub' AND m.role = 'owner')
  OR (tenant_id = current_setting('gregale.team_receipt_read', true)
    AND actor = current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
);
CREATE POLICY scoped_event_creates ON api.team_membership_events FOR INSERT WITH CHECK (
  tenant_id = current_setting('gregale.team_member_write', true)
  AND actor = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
);
-- No HTTP update/delete policy: the event doubles as the immutable receipt.

CREATE FUNCTION api.list_team_members(team_id text, after_subject text DEFAULT '', page_size integer DEFAULT 20)
RETURNS SETOF api.team_members LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE
  caller_subject text := current_setting('request.jwt.claims', true)::jsonb ->> 'sub';
  previous_scope text := current_setting('gregale.team_member_read', true);
BEGIN
  IF page_size IS NULL OR page_size < 1 OR page_size > 100 OR after_subject IS NULL THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'invalid_member_page';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM api.team_members m WHERE m.tenant_id = team_id AND m.subject = caller_subject AND m.role = 'owner') THEN
    RAISE SQLSTATE '42501' USING MESSAGE = 'team_owner_required';
  END IF;
  PERFORM set_config('gregale.team_member_read', team_id, true);
  RETURN QUERY SELECT m.* FROM api.team_members m WHERE m.tenant_id = team_id AND m.subject > after_subject ORDER BY m.subject LIMIT page_size + 1;
  PERFORM set_config('gregale.team_member_read', COALESCE(previous_scope, ''), true);
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.team_member_read', COALESCE(previous_scope, ''), true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.list_team_members(text,text,integer) FROM PUBLIC;
COMMENT ON FUNCTION api.list_team_members(text,text,integer) IS '@gregale:rpc';

-- Exact-key lookup lets the original actor resolve a committed operation after
-- transfer, demotion or removal, without granting directory or history access.
CREATE FUNCTION api.get_team_change_receipt(team_id text, operation_key uuid)
RETURNS SETOF api.team_membership_events LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE
  caller_subject text := current_setting('request.jwt.claims', true)::jsonb ->> 'sub';
  previous_scope text := current_setting('gregale.team_receipt_read', true);
BEGIN
  PERFORM set_config('gregale.team_receipt_read', COALESCE(team_id, ''), true);
  RETURN QUERY SELECT e.* FROM api.team_membership_events e
    WHERE e.tenant_id = team_id AND e.actor = caller_subject AND e.request_key = operation_key;
  PERFORM set_config('gregale.team_receipt_read', COALESCE(previous_scope, ''), true);
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.team_receipt_read', COALESCE(previous_scope, ''), true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.get_team_change_receipt(text,uuid) FROM PUBLIC;
COMMENT ON FUNCTION api.get_team_change_receipt(text,uuid) IS '@gregale:rpc';

CREATE FUNCTION api.change_team_member_once(request_key uuid, team_id text, target_subject text, expected_revision uuid, new_role api.team_role)
RETURNS SETOF api.team_membership_events LANGUAGE plpgsql SECURITY INVOKER
SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text := NULLIF(current_setting('request.jwt.claims', true)::jsonb ->> 'sub', '');
  previous_read text := current_setting('gregale.team_member_read', true);
  previous_write text := current_setting('gregale.team_member_write', true);
  previous_receipt text := current_setting('gregale.team_receipt_read', true);
  actor_row api.team_members;
  target_row api.team_members;
  changed api.team_members;
  actor_after api.team_members;
  result api.team_membership_events;
  intended jsonb;
BEGIN
  IF caller_subject IS NULL OR request_key IS NULL OR team_id IS NULL OR team_id = '' OR target_subject IS NULL OR target_subject = '' OR length(target_subject) > 256 OR (expected_revision IS NULL AND new_role IS NULL) THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'invalid_membership_change';
  END IF;
  IF current_setting('transaction_isolation') <> 'read committed' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'membership_read_committed_required';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || team_id, 0));
  -- Recheck authority after waiting: another owner may have revoked this actor.
  SELECT m.* INTO actor_row FROM api.team_members m WHERE m.tenant_id = team_id AND m.subject = caller_subject AND m.role = 'owner';
  IF NOT FOUND THEN RAISE SQLSTATE '42501' USING MESSAGE = 'team_owner_required'; END IF;
  PERFORM set_config('gregale.team_member_read', team_id, true);
  PERFORM set_config('gregale.team_member_write', team_id, true);
  PERFORM set_config('gregale.team_receipt_read', team_id, true);
  intended := jsonb_build_object('kind', 'change', 'target', target_subject, 'revision', expected_revision, 'role', new_role);
  SELECT e.* INTO result FROM api.team_membership_events e WHERE e.tenant_id = team_id AND e.actor = caller_subject AND e.request_key = change_team_member_once.request_key;
  IF FOUND THEN
    IF result.payload <> intended THEN RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict'; END IF;
  ELSE
    IF NOT EXISTS (SELECT 1 FROM api.team_users u WHERE u.subject = target_subject) THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE = 'unknown_team_user';
    END IF;
    SELECT m.* INTO target_row FROM api.team_members m WHERE m.tenant_id = team_id AND m.subject = target_subject;
    IF target_row.revision IS DISTINCT FROM expected_revision THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'membership_revision_conflict';
    END IF;
    IF target_row.role IS NOT DISTINCT FROM new_role THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE = 'membership_unchanged';
    END IF;
    IF expected_revision IS NULL THEN
      INSERT INTO api.team_members(tenant_id, subject, role) VALUES(team_id, target_subject, new_role) RETURNING * INTO changed;
    ELSIF new_role IS NULL THEN
      DELETE FROM api.team_members m WHERE m.tenant_id = team_id AND m.subject = target_subject;
    ELSE
      UPDATE api.team_members m SET role = new_role WHERE m.tenant_id = team_id AND m.subject = target_subject RETURNING m.* INTO changed;
    END IF;
    SELECT m.* INTO actor_after FROM api.team_members m WHERE m.tenant_id = team_id AND m.subject = caller_subject;
    INSERT INTO api.team_membership_events(tenant_id, actor, request_key, action, target_subject,
      before_role, after_role, before_revision, after_revision, actor_before_revision, actor_after_revision, actor_after_role, payload)
    VALUES(team_id, caller_subject, request_key, CASE WHEN expected_revision IS NULL THEN 'add'::api.team_change_action WHEN new_role IS NULL THEN 'remove'::api.team_change_action ELSE 'change'::api.team_change_action END, target_subject,
      target_row.role, changed.role, target_row.revision, changed.revision, actor_row.revision, actor_after.revision, actor_after.role, intended) RETURNING * INTO result;
  END IF;
  PERFORM set_config('gregale.team_member_read', COALESCE(previous_read, ''), true);
  PERFORM set_config('gregale.team_member_write', COALESCE(previous_write, ''), true);
  PERFORM set_config('gregale.team_receipt_read', COALESCE(previous_receipt, ''), true);
  RETURN NEXT result;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.team_member_read', COALESCE(previous_read, ''), true);
  PERFORM set_config('gregale.team_member_write', COALESCE(previous_write, ''), true);
  PERFORM set_config('gregale.team_receipt_read', COALESCE(previous_receipt, ''), true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.change_team_member_once(uuid,text,text,uuid,api.team_role) FROM PUBLIC;
COMMENT ON FUNCTION api.change_team_member_once(uuid,text,text,uuid,api.team_role) IS '@gregale:rpc';

CREATE FUNCTION api.transfer_team_ownership_once(request_key uuid, team_id text, target_subject text, expected_revision uuid)
RETURNS SETOF api.team_membership_events LANGUAGE plpgsql SECURITY INVOKER
SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text := NULLIF(current_setting('request.jwt.claims', true)::jsonb ->> 'sub', '');
  previous_read text := current_setting('gregale.team_member_read', true);
  previous_write text := current_setting('gregale.team_member_write', true);
  previous_receipt text := current_setting('gregale.team_receipt_read', true);
  actor_row api.team_members;
  target_row api.team_members;
  changed api.team_members;
  actor_after api.team_members;
  result api.team_membership_events;
  intended jsonb;
BEGIN
  IF caller_subject IS NULL OR request_key IS NULL OR team_id IS NULL OR team_id = '' OR target_subject IS NULL OR target_subject = '' OR length(target_subject) > 256 OR target_subject = caller_subject OR expected_revision IS NULL THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'invalid_membership_change';
  END IF;
  IF current_setting('transaction_isolation') <> 'read committed' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'membership_read_committed_required';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || team_id, 0));
  -- Recheck authority after waiting: another owner may have revoked this actor.
  SELECT m.* INTO actor_row FROM api.team_members m WHERE m.tenant_id = team_id AND m.subject = caller_subject AND m.role = 'owner';
  IF NOT FOUND THEN RAISE SQLSTATE '42501' USING MESSAGE = 'team_owner_required'; END IF;
  PERFORM set_config('gregale.team_member_read', team_id, true);
  PERFORM set_config('gregale.team_member_write', team_id, true);
  PERFORM set_config('gregale.team_receipt_read', team_id, true);
  intended := jsonb_build_object('kind', 'transfer', 'target', target_subject, 'revision', expected_revision, 'role', 'owner'::api.team_role);
  SELECT e.* INTO result FROM api.team_membership_events e WHERE e.tenant_id = team_id AND e.actor = caller_subject AND e.request_key = transfer_team_ownership_once.request_key;
  IF FOUND THEN
    IF result.payload <> intended THEN RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict'; END IF;
  ELSE
    IF NOT EXISTS (SELECT 1 FROM api.team_users u WHERE u.subject = target_subject) THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE = 'unknown_team_user';
    END IF;
    SELECT m.* INTO target_row FROM api.team_members m WHERE m.tenant_id = team_id AND m.subject = target_subject;
    IF target_row.revision IS DISTINCT FROM expected_revision THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'membership_revision_conflict';
    END IF;
    -- Promote first; demotion can never observe an ownerless team.
    UPDATE api.team_members m SET role = 'owner' WHERE m.tenant_id = team_id AND m.subject = target_subject RETURNING m.* INTO changed;
    UPDATE api.team_members m SET role = 'admin' WHERE m.tenant_id = team_id AND m.subject = caller_subject RETURNING m.* INTO actor_after;
    INSERT INTO api.team_membership_events(tenant_id, actor, request_key, action, target_subject,
      before_role, after_role, before_revision, after_revision, actor_before_revision, actor_after_revision, actor_after_role, payload)
    VALUES(team_id, caller_subject, request_key, 'transfer'::api.team_change_action, target_subject,
      target_row.role, changed.role, target_row.revision, changed.revision, actor_row.revision, actor_after.revision, actor_after.role, intended) RETURNING * INTO result;
  END IF;
  PERFORM set_config('gregale.team_member_read', COALESCE(previous_read, ''), true);
  PERFORM set_config('gregale.team_member_write', COALESCE(previous_write, ''), true);
  PERFORM set_config('gregale.team_receipt_read', COALESCE(previous_receipt, ''), true);
  RETURN NEXT result;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.team_member_read', COALESCE(previous_read, ''), true);
  PERFORM set_config('gregale.team_member_write', COALESCE(previous_write, ''), true);
  PERFORM set_config('gregale.team_receipt_read', COALESCE(previous_receipt, ''), true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.transfer_team_ownership_once(uuid,text,text,uuid) FROM PUBLIC;
COMMENT ON FUNCTION api.transfer_team_ownership_once(uuid,text,text,uuid) IS '@gregale:rpc';
