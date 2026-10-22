-- Customer-owned published views; personal preferences remain private.
CREATE TABLE api.team_shared_order_views (
  view_id uuid PRIMARY KEY,
  tenant_id text NOT NULL,
  name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
  status_filter api.order_status,
  minimum_total integer CHECK(minimum_total>=0),
  maximum_total integer CHECK(maximum_total>=0),
  sort_order text NOT NULL CHECK(sort_order IN ('id_asc','id_desc','total_asc','total_desc')),
  revision uuid NOT NULL DEFAULT gen_random_uuid(),
  version integer NOT NULL DEFAULT 1 CHECK(version>0),
  changed_by text NOT NULL,
  changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK(minimum_total IS NULL OR maximum_total IS NULL OR minimum_total<=maximum_total),
  UNIQUE(tenant_id,name)
);
CREATE TABLE api.team_shared_view_operations (
  tenant_id text NOT NULL,
  actor text NOT NULL,
  request_id uuid NOT NULL,
  view_id uuid NOT NULL,
  action text NOT NULL CHECK(action IN ('publish','update','copy')),
  payload jsonb NOT NULL,
  shared_snapshot jsonb NOT NULL,
  personal_snapshot jsonb,
  occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY(tenant_id,actor,request_id),
  CHECK((action='copy')=(personal_snapshot IS NOT NULL))
);
CREATE INDEX shared_view_browse ON api.team_shared_order_views(tenant_id,view_id);
CREATE INDEX shared_view_history ON api.team_shared_view_operations(tenant_id,view_id,request_id);
ALTER TABLE api.team_shared_order_views ENABLE ROW LEVEL SECURITY;
ALTER TABLE api.team_shared_view_operations ENABLE ROW LEVEL SECURITY;
CREATE POLICY member_shared_view_reads ON api.team_shared_order_views FOR SELECT USING(
  EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_shared_order_views.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')
);
CREATE POLICY reviewed_shared_view_creates ON api.team_shared_order_views FOR INSERT WITH CHECK(tenant_id=current_setting('gregale.shared_view_write',true));
CREATE POLICY reviewed_shared_view_updates ON api.team_shared_order_views FOR UPDATE
  USING(tenant_id=current_setting('gregale.shared_view_write',true)) WITH CHECK(tenant_id=current_setting('gregale.shared_view_write',true));
CREATE POLICY member_shared_view_history ON api.team_shared_view_operations FOR SELECT USING(
  (action<>'copy' OR actor=current_setting('request.jwt.claims',true)::jsonb->>'sub')
  AND EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_shared_view_operations.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')
);
CREATE POLICY actor_shared_view_receipts ON api.team_shared_view_operations FOR SELECT USING(
  tenant_id=current_setting('gregale.shared_view_receipt',true) AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub'
);
CREATE POLICY reviewed_shared_view_operations ON api.team_shared_view_operations FOR INSERT WITH CHECK(
  tenant_id=current_setting('gregale.shared_view_write',true) AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub'
);
CREATE FUNCTION api.get_shared_order_view_receipt(team_id text,operation_key uuid) RETURNS SETOF api.team_shared_view_operations
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE old_scope text:=current_setting('gregale.shared_view_receipt',true);
BEGIN
  PERFORM set_config('gregale.shared_view_receipt',COALESCE(team_id,''),true);
  RETURN QUERY SELECT r.* FROM api.team_shared_view_operations r WHERE r.tenant_id=team_id AND r.actor=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND r.request_id=operation_key;
  PERFORM set_config('gregale.shared_view_receipt',COALESCE(old_scope,''),true);
EXCEPTION WHEN OTHERS THEN PERFORM set_config('gregale.shared_view_receipt',COALESCE(old_scope,''),true);RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.get_shared_order_view_receipt(text,uuid) FROM PUBLIC;
COMMENT ON FUNCTION api.get_shared_order_view_receipt(text,uuid) IS '@gregale:rpc';

CREATE FUNCTION api.save_shared_order_view_once(team_id text,request_id uuid,view_id uuid,expected_revision uuid,view_name text,
  status_filter api.order_status,minimum_total integer,maximum_total integer,sort_order text)
RETURNS SETOF api.team_shared_view_operations LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text:=NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
  old_scope text:=current_setting('gregale.shared_view_write',true);
  current_view api.team_shared_order_views;
  receipt api.team_shared_view_operations;
  intended jsonb;
BEGIN
  IF caller_subject IS NULL OR team_id IS NULL OR team_id='' OR request_id IS NULL OR view_id IS NULL OR view_name IS NULL OR length(view_name) NOT BETWEEN 1 AND 80
    OR view_name<>btrim(view_name) OR btrim(view_name)='' OR minimum_total<0 OR maximum_total<0 OR minimum_total>maximum_total OR sort_order IS NULL OR sort_order NOT IN ('id_asc','id_desc','total_asc','total_desc') THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_shared_view'; END IF;
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='order_read_committed_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:'||team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=caller_subject AND m.role IN ('owner','admin')) THEN RAISE SQLSTATE '42501' USING MESSAGE='team_write_denied'; END IF;
  intended:=jsonb_build_object('kind','publish','id',view_id,'revision',expected_revision,'name',view_name,'status',status_filter,'minimum',minimum_total,'maximum',maximum_total,'sort',sort_order);
  SELECT r.* INTO receipt FROM api.team_shared_view_operations r WHERE r.tenant_id=team_id AND r.actor=caller_subject AND r.request_id=save_shared_order_view_once.request_id;
  IF FOUND THEN
    IF receipt.action='copy' OR receipt.payload<>intended THEN RAISE SQLSTATE 'PT409' USING MESSAGE='shared_view_payload_conflict'; END IF;
  ELSE
    SELECT v.* INTO current_view FROM api.team_shared_order_views v WHERE v.tenant_id=team_id AND v.view_id=save_shared_order_view_once.view_id;
    IF current_view.revision IS DISTINCT FROM expected_revision THEN RAISE SQLSTATE 'PT409' USING MESSAGE='shared_view_revision_conflict'; END IF;
    PERFORM set_config('gregale.shared_view_write',team_id,true);
    IF current_view.view_id IS NULL THEN
      INSERT INTO api.team_shared_order_views(view_id,tenant_id,name,status_filter,minimum_total,maximum_total,sort_order,changed_by)
      VALUES(view_id,team_id,view_name,status_filter,minimum_total,maximum_total,sort_order,caller_subject) RETURNING * INTO current_view;
    ELSE
      UPDATE api.team_shared_order_views v SET name=view_name,status_filter=save_shared_order_view_once.status_filter,minimum_total=save_shared_order_view_once.minimum_total,maximum_total=save_shared_order_view_once.maximum_total,sort_order=save_shared_order_view_once.sort_order,revision=gen_random_uuid(),version=v.version+1,changed_by=caller_subject,changed_at=clock_timestamp()
      WHERE v.view_id=save_shared_order_view_once.view_id AND v.tenant_id=team_id RETURNING v.* INTO current_view;
    END IF;
    INSERT INTO api.team_shared_view_operations(tenant_id,actor,request_id,view_id,action,payload,shared_snapshot)
      VALUES(team_id,caller_subject,request_id,view_id,CASE WHEN expected_revision IS NULL THEN 'publish' ELSE 'update' END,intended,to_jsonb(current_view)) RETURNING * INTO receipt;
  END IF;
  PERFORM set_config('gregale.shared_view_write',COALESCE(old_scope,''),true);RETURN NEXT receipt;
EXCEPTION WHEN OTHERS THEN PERFORM set_config('gregale.shared_view_write',COALESCE(old_scope,''),true);RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.save_shared_order_view_once(text,uuid,uuid,uuid,text,api.order_status,integer,integer,text) FROM PUBLIC;
COMMENT ON FUNCTION api.save_shared_order_view_once(text,uuid,uuid,uuid,text,api.order_status,integer,integer,text) IS '@gregale:rpc';

CREATE FUNCTION api.copy_shared_order_view_once(team_id text,request_id uuid,shared_view_id uuid,expected_revision uuid,personal_view_id uuid,personal_name text)
RETURNS SETOF api.team_shared_view_operations LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text:=NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
  old_scope text:=current_setting('gregale.shared_view_write',true);
  current_view api.team_shared_order_views;
  receipt api.team_shared_view_operations;
  personal_receipt api.team_order_view_receipts;
  intended jsonb;
BEGIN
  IF caller_subject IS NULL OR team_id IS NULL OR team_id='' OR request_id IS NULL OR shared_view_id IS NULL OR expected_revision IS NULL OR personal_view_id IS NULL OR personal_name IS NULL
    OR length(personal_name) NOT BETWEEN 1 AND 80 OR personal_name<>btrim(personal_name) OR btrim(personal_name)='' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_shared_view'; END IF;
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='order_read_committed_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:'||team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=caller_subject) THEN RAISE SQLSTATE '42501' USING MESSAGE='team_membership_required'; END IF;
  intended:=jsonb_build_object('kind','copy','id',shared_view_id,'revision',expected_revision,'personal_id',personal_view_id,'name',personal_name);
  SELECT r.* INTO receipt FROM api.team_shared_view_operations r WHERE r.tenant_id=team_id AND r.actor=caller_subject AND r.request_id=copy_shared_order_view_once.request_id;
  IF FOUND THEN
    IF receipt.action<>'copy' OR receipt.payload<>intended THEN RAISE SQLSTATE 'PT409' USING MESSAGE='shared_view_payload_conflict'; END IF;
  ELSE
    SELECT v.* INTO current_view FROM api.team_shared_order_views v WHERE v.tenant_id=team_id AND v.view_id=shared_view_id;
    IF current_view.revision IS DISTINCT FROM expected_revision THEN RAISE SQLSTATE 'PT409' USING MESSAGE='shared_view_revision_conflict'; END IF;
    -- A copy creates a separate personal view, never an overwrite or a live link.
    IF EXISTS(SELECT 1 FROM api.team_order_view_receipts r WHERE r.tenant_id=team_id AND r.actor=caller_subject AND r.request_id=copy_shared_order_view_once.request_id) THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE='shared_view_payload_conflict'; END IF;
    SELECT r.* INTO personal_receipt FROM api.save_order_view_once(team_id,request_id,personal_view_id,NULL,personal_name,current_view.status_filter,current_view.minimum_total,current_view.maximum_total,current_view.sort_order) r;
    PERFORM set_config('gregale.shared_view_write',team_id,true);
    INSERT INTO api.team_shared_view_operations(tenant_id,actor,request_id,view_id,action,payload,shared_snapshot,personal_snapshot)
      VALUES(team_id,caller_subject,request_id,shared_view_id,'copy',intended,to_jsonb(current_view),personal_receipt.snapshot) RETURNING * INTO receipt;
  END IF;
  PERFORM set_config('gregale.shared_view_write',COALESCE(old_scope,''),true);RETURN NEXT receipt;
EXCEPTION WHEN OTHERS THEN PERFORM set_config('gregale.shared_view_write',COALESCE(old_scope,''),true);RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.copy_shared_order_view_once(text,uuid,uuid,uuid,uuid,text) FROM PUBLIC;
COMMENT ON FUNCTION api.copy_shared_order_view_once(text,uuid,uuid,uuid,uuid,text) IS '@gregale:rpc';
