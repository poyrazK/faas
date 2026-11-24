-- filename: 20261124124546100_telemetry_coverage_clean_restarts.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- Request telemetry coverage that survives a clean gateway restart, and
-- coverage gaps that mean lost rows (2026-11-24 telemetry audit).
--
-- 1. A gateway's coverage was one process lifetime: every restart (about ten a
--    day per node on prod) began a new healthy_since, so no analytics window
--    spanning a restart was ever complete, and the release guards that ask
--    whether a gateway was healthy since a fixed start (canary breaker, 5xx
--    rollback) held until an operator stepped in. A publisher that drained or
--    journaled every row now ends with a stopped heartbeat; the next boot of
--    the same node, heartbeating within the silence grace with no new
--    unattributed loss, keeps the old healthy_since.
-- 2. A gap row was written whenever an app had rows in flight at heartbeat
--    time, which is normal, so every busy app read "window incomplete" for
--    the retention period. A gap is now written only when the app's dropped
--    total grows; in-flight rows stay visible as telemetry_ingestion_pending
--    while they are pending. pending_since records when an app's backlog
--    began (the node's previous heartbeat, which had not counted it), so a
--    loss marks the whole backlog and a long backlog holds every window it
--    touches, while earlier windows keep their proof.
-- The checks are validated by the next migration.
-- Re-runnable: columns and checks are added only when missing.
ALTER TABLE request_telemetry_coverage ADD COLUMN IF NOT EXISTS clean_stop_at timestamptz;
ALTER TABLE request_analytics_app_gaps ADD COLUMN IF NOT EXISTS dropped_total bigint NOT NULL DEFAULT 0;
ALTER TABLE request_telemetry_app_gaps ADD COLUMN IF NOT EXISTS pending_since timestamptz;
ALTER TABLE request_analytics_app_gaps ADD COLUMN IF NOT EXISTS pending_since timestamptz;
DO $checks$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='request_telemetry_coverage_clean_stop_at_check' AND conrelid='request_telemetry_coverage'::regclass) THEN
  ALTER TABLE request_telemetry_coverage ADD CONSTRAINT request_telemetry_coverage_clean_stop_at_check
   CHECK (clean_stop_at IS NULL OR clean_stop_at <= received_at) NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='request_analytics_app_gaps_dropped_total_check' AND conrelid='request_analytics_app_gaps'::regclass) THEN
  ALTER TABLE request_analytics_app_gaps ADD CONSTRAINT request_analytics_app_gaps_dropped_total_check
   CHECK (dropped_total >= 0) NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='request_telemetry_app_gaps_pending_since_check' AND conrelid='request_telemetry_app_gaps'::regclass) THEN
  ALTER TABLE request_telemetry_app_gaps ADD CONSTRAINT request_telemetry_app_gaps_pending_since_check
   CHECK (pending_since IS NULL OR (pending_count > 0 AND pending_since <= updated_at)) NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='request_analytics_app_gaps_pending_since_check' AND conrelid='request_analytics_app_gaps'::regclass) THEN
  ALTER TABLE request_analytics_app_gaps ADD CONSTRAINT request_analytics_app_gaps_pending_since_check
   CHECK (pending_since IS NULL OR (pending_count > 0 AND pending_since <= updated_at)) NOT VALID;
 END IF;
END $checks$;

CREATE OR REPLACE FUNCTION record_telemetry_coverage(node text, boot uuid, seq bigint, is_enabled boolean, sampling integer, drops bigint, pending integer, source_stamp timestamptz, scoped boolean, unattributed_drops bigint, app_gaps jsonb, stopped boolean) RETURNS void LANGUAGE plpgsql AS $$
DECLARE stamp timestamptz:=clock_timestamp(); attributed_pending bigint; attributed_losses bigint; gap record; backlog_from timestamptz;
BEGIN
 IF stopped IS NULL THEN RAISE EXCEPTION 'invalid telemetry coverage stop flag' USING ERRCODE='22023'; END IF;
 IF jsonb_typeof(app_gaps) IS DISTINCT FROM 'array' OR (NOT scoped AND jsonb_array_length(app_gaps)>0) THEN
 RAISE EXCEPTION 'invalid app telemetry coverage' USING ERRCODE='22023'; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_to_recordset(app_gaps) g(app_id uuid,dropped_count bigint,pending_count integer) WHERE app_id IS NULL OR app_id='00000000-0000-0000-0000-000000000000'::uuid OR dropped_count IS NULL OR dropped_count<0 OR pending_count IS NULL OR pending_count<0) OR
 EXISTS(SELECT 1 FROM jsonb_to_recordset(app_gaps) g(app_id uuid) GROUP BY app_id HAVING count(*)>1) THEN
 RAISE EXCEPTION 'invalid or duplicated app telemetry gap' USING ERRCODE='22023'; END IF;
 SELECT coalesce(sum(pending_count),0),coalesce(sum(dropped_count),0) INTO attributed_pending,attributed_losses FROM jsonb_to_recordset(app_gaps) g(pending_count integer,dropped_count bigint);
 IF attributed_pending>pending THEN RAISE EXCEPTION 'invalid attributed backlog' USING ERRCODE='22023'; END IF;
 IF source_stamp>stamp+interval '5 seconds' OR source_stamp<stamp-interval '30 seconds' THEN
 RAISE EXCEPTION 'telemetry coverage clock is stale or in the future' USING ERRCODE='22023'; END IF;
 -- FK app locks precede the node lock, matching promotion's app-before-node
 -- ordering. The writer cannot hold a node while waiting on a promotion app.
 PERFORM 1 FROM apps a WHERE a.id IN (SELECT app_id FROM jsonb_to_recordset(app_gaps) g(app_id uuid)) ORDER BY a.id FOR KEY SHARE;
 INSERT INTO request_telemetry_coverage AS old(node_name,boot_id,sequence,enabled,sampling_basis_points,dropped_total,pending_count,source_at,received_at,healthy_since,app_scoped,unattributed_dropped_total,unattributed_pending_count,clean_stop_at)
 VALUES(node,boot,seq,is_enabled,sampling,drops,pending,source_stamp,stamp,
 CASE WHEN is_enabled AND sampling=10000 AND (CASE WHEN scoped THEN unattributed_drops=0 AND pending=attributed_pending ELSE drops=0 AND pending=0 END) THEN stamp END,
 scoped,unattributed_drops,(pending-attributed_pending)::integer,CASE WHEN stopped THEN stamp END)
 ON CONFLICT(node_name) DO UPDATE SET boot_id=excluded.boot_id,sequence=excluded.sequence,enabled=excluded.enabled,sampling_basis_points=excluded.sampling_basis_points,
 dropped_total=excluded.dropped_total,pending_count=excluded.pending_count,source_at=excluded.source_at,received_at=excluded.received_at,
 app_scoped=excluded.app_scoped,unattributed_dropped_total=excluded.unattributed_dropped_total,unattributed_pending_count=excluded.unattributed_pending_count,
 clean_stop_at=excluded.clean_stop_at,
 healthy_since=CASE WHEN NOT excluded.enabled OR excluded.sampling_basis_points<>10000 OR (CASE WHEN excluded.app_scoped THEN excluded.unattributed_pending_count ELSE excluded.pending_count END)<>0 THEN NULL
 -- A new boot continues the coverage of a predecessor that stopped cleanly
 -- (every row delivered or journaled) within the silence grace, provided it
 -- has lost nothing it cannot attribute. Any other new boot starts fresh.
 WHEN old.boot_id<>excluded.boot_id THEN
  CASE WHEN old.clean_stop_at IS NOT NULL AND old.clean_stop_at>=stamp-interval '30 seconds' AND old.healthy_since IS NOT NULL
    AND (CASE WHEN excluded.app_scoped THEN excluded.unattributed_dropped_total=0 ELSE excluded.dropped_total=0 END)
   THEN old.healthy_since ELSE stamp END
 WHEN old.received_at<stamp-interval '30 seconds' OR excluded.dropped_total<old.dropped_total OR
 (excluded.app_scoped AND excluded.dropped_total-old.dropped_total>attributed_losses+greatest(excluded.unattributed_dropped_total-old.unattributed_dropped_total,0)) OR (CASE WHEN excluded.app_scoped THEN old.unattributed_dropped_total<>excluded.unattributed_dropped_total ELSE old.dropped_total<>excluded.dropped_total END) THEN stamp
 ELSE coalesce(old.healthy_since,stamp) END
 WHERE excluded.source_at>old.source_at AND (old.boot_id<>excluded.boot_id OR excluded.sequence>old.sequence);
 IF NOT FOUND THEN RAISE EXCEPTION 'telemetry coverage replay or out-of-order heartbeat' USING ERRCODE='22023'; END IF;
 -- Omitted apps have no current backlog. Recovery starts at this receipt,
 -- never at the previous unhealthy heartbeat. Reported apps are left to the
 -- upsert, so the gap trigger still sees when their backlog began.
 UPDATE request_telemetry_app_gaps SET last_gap_at=stamp,pending_count=0,pending_since=NULL,updated_at=stamp WHERE node_name=node AND pending_count>0
  AND app_id NOT IN (SELECT g.app_id FROM jsonb_to_recordset(app_gaps) g(app_id uuid,dropped_count bigint,pending_count integer)
   JOIN apps a ON a.id=g.app_id AND a.status<>'deleted' WHERE g.dropped_count>0 OR g.pending_count>0);
 -- A backlog that begins now holds rows the previous heartbeat did not
 -- count as pending, so it reaches back to that heartbeat and no further.
 SELECT least(previous_source_at,stamp) INTO backlog_from FROM request_analytics_coverage_watermarks WHERE node_name=node AND source_kind='raw';
 backlog_from:=coalesce(backlog_from,stamp);
 FOR gap IN SELECT * FROM jsonb_to_recordset(app_gaps) g(app_id uuid,dropped_count bigint,pending_count integer) ORDER BY app_id LOOP
 IF (gap.dropped_count>0 OR gap.pending_count>0) AND EXISTS(SELECT 1 FROM apps WHERE id=gap.app_id AND status<>'deleted') THEN
 INSERT INTO request_telemetry_app_gaps AS old(node_name,app_id,last_gap_at,pending_count,pending_since,dropped_total,updated_at)
 VALUES(node,gap.app_id,stamp,gap.pending_count,CASE WHEN gap.pending_count>0 THEN backlog_from END,gap.dropped_count,stamp)
 ON CONFLICT(node_name,app_id) DO UPDATE SET last_gap_at=stamp,pending_count=excluded.pending_count,
  pending_since=CASE WHEN excluded.pending_count>0 THEN coalesce(old.pending_since,CASE WHEN old.pending_count>0 THEN old.last_gap_at END,backlog_from) END,
  dropped_total=old.dropped_total+excluded.dropped_total,updated_at=stamp;
 END IF;
 END LOOP;
END $$;

-- N-1 apid calls the eleven-argument form: a heartbeat that never says it
-- stopped, which keeps the old per-boot coverage.
CREATE OR REPLACE FUNCTION record_telemetry_coverage(node text, boot uuid, seq bigint, is_enabled boolean, sampling integer, drops bigint, pending integer, source_stamp timestamptz, scoped boolean, unattributed_drops bigint, app_gaps jsonb) RETURNS void LANGUAGE sql AS $$
 SELECT record_telemetry_coverage(node,boot,seq,is_enabled,sampling,drops,pending,source_stamp,scoped,unattributed_drops,app_gaps,false)
$$;

CREATE OR REPLACE FUNCTION record_request_analytics_coverage(node text, boot uuid, counting boolean, unattributed_drops bigint, app_gaps jsonb) RETURNS void LANGUAGE plpgsql AS $$
DECLARE stamp timestamptz:=clock_timestamp(); gap record; backlog_from timestamptz;
BEGIN
 IF jsonb_typeof(app_gaps) IS DISTINCT FROM 'array' OR unattributed_drops IS NULL OR unattributed_drops<0 OR
  EXISTS(SELECT 1 FROM jsonb_to_recordset(app_gaps) g(app_id uuid,analytics_dropped_count bigint,analytics_pending_count integer)
   WHERE app_id IS NULL OR coalesce(analytics_dropped_count,0)<0 OR coalesce(analytics_pending_count,0)<0) THEN
 RAISE EXCEPTION 'invalid analytics coverage' USING ERRCODE='22023'; END IF;
 INSERT INTO request_analytics_node_coverage AS old(node_name,boot_id,counting_since,heartbeat_at,unattributed_dropped_total)
 VALUES(node,boot,CASE WHEN counting THEN stamp END,stamp,unattributed_drops)
 ON CONFLICT(node_name) DO UPDATE SET boot_id=excluded.boot_id,heartbeat_at=stamp,unattributed_dropped_total=excluded.unattributed_dropped_total,
 counting_since=CASE WHEN NOT counting THEN NULL
  WHEN old.counting_since IS NULL OR old.boot_id<>excluded.boot_id OR old.heartbeat_at<stamp-interval '30 seconds'
   OR excluded.unattributed_dropped_total<>old.unattributed_dropped_total THEN stamp
  ELSE old.counting_since END;
 -- Omitted apps have nothing late any more; reported ones are left to the
 -- upsert, so the gap trigger still sees when their backlog began.
 UPDATE request_analytics_app_gaps SET last_gap_at=stamp,pending_count=0,pending_since=NULL,updated_at=stamp WHERE node_name=node AND pending_count>0
  AND app_id NOT IN (SELECT g.app_id FROM jsonb_to_recordset(app_gaps) g(app_id uuid,analytics_dropped_count bigint,analytics_pending_count integer)
   JOIN apps a ON a.id=g.app_id AND a.status<>'deleted' WHERE coalesce(g.analytics_dropped_count,0)>0 OR coalesce(g.analytics_pending_count,0)>0);
 SELECT least(previous_source_at,stamp) INTO backlog_from FROM request_analytics_coverage_watermarks WHERE node_name=node AND source_kind='analytics';
 backlog_from:=coalesce(backlog_from,stamp);
 FOR gap IN SELECT g.app_id,coalesce(g.analytics_dropped_count,0) AS dropped,coalesce(g.analytics_pending_count,0) AS pending
  FROM jsonb_to_recordset(app_gaps) g(app_id uuid,analytics_dropped_count bigint,analytics_pending_count integer) ORDER BY g.app_id LOOP
 IF (gap.dropped>0 OR gap.pending>0) AND EXISTS(SELECT 1 FROM apps WHERE id=gap.app_id AND status<>'deleted') THEN
 -- The dropped total is cumulative: the gap trigger marks a loss only when
 -- it grows, not for counts still in flight.
 INSERT INTO request_analytics_app_gaps AS old(node_name,app_id,last_gap_at,pending_count,pending_since,dropped_total,updated_at)
 VALUES(node,gap.app_id,stamp,gap.pending,CASE WHEN gap.pending>0 THEN backlog_from END,gap.dropped,stamp)
 ON CONFLICT(node_name,app_id) DO UPDATE SET last_gap_at=stamp,pending_count=excluded.pending_count,
  pending_since=CASE WHEN excluded.pending_count>0 THEN coalesce(old.pending_since,CASE WHEN old.pending_count>0 THEN old.last_gap_at END,backlog_from) END,
  dropped_total=old.dropped_total+excluded.dropped_total,updated_at=stamp;
 END IF;
 END LOOP;
END $$;

-- A loss marks the app's evidence from the start of the backlog it was lost
-- from, or else from the node's previous heartbeat: a lost row was pending
-- then or arrived after it, and windows proven before it keep their proof.
CREATE OR REPLACE FUNCTION request_analytics_record_coverage_gap() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE kind text:=CASE WHEN TG_TABLE_NAME='request_analytics_app_gaps' THEN 'analytics' ELSE 'raw' END;
 lost bigint:=NEW.dropped_total; first_gap timestamptz:=least(NEW.updated_at,NEW.last_gap_at);
BEGIN
 SELECT least(first_gap,previous_source_at) INTO first_gap FROM request_analytics_coverage_watermarks WHERE node_name=NEW.node_name AND source_kind=kind;
 first_gap:=least(coalesce(first_gap,NEW.updated_at),coalesce(NEW.pending_since,NEW.updated_at));
 IF TG_OP='UPDATE' THEN
  lost:=NEW.dropped_total-OLD.dropped_total;
  IF OLD.pending_count>0 THEN first_gap:=least(first_gap,coalesce(OLD.pending_since,OLD.last_gap_at)); END IF;
 END IF;
 IF lost<=0 THEN RETURN NEW; END IF;
 INSERT INTO request_analytics_coverage_gaps(node_name,source_kind,app_id,gap_from,gap_until)
 VALUES(NEW.node_name,kind,NEW.app_id,first_gap,NEW.updated_at)
 ON CONFLICT(node_name,source_kind,app_id,gap_from) DO UPDATE SET gap_until=greatest(request_analytics_coverage_gaps.gap_until,EXCLUDED.gap_until);
 RETURN NEW;
END $$;

-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION request_analytics_record_coverage_gap() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE first_gap timestamptz:=NEW.last_gap_at; kind text:=CASE WHEN TG_TABLE_NAME='request_analytics_app_gaps' THEN 'analytics' ELSE 'raw' END;
BEGIN
 SELECT least(first_gap,previous_source_at) INTO first_gap FROM request_analytics_coverage_watermarks WHERE node_name=NEW.node_name AND source_kind=kind;
 first_gap:=coalesce(first_gap,NEW.last_gap_at);
 IF TG_OP='UPDATE' AND OLD.pending_count>0 THEN first_gap:=least(first_gap,OLD.last_gap_at); END IF;
 INSERT INTO request_analytics_coverage_gaps(node_name,source_kind,app_id,gap_from,gap_until)
 VALUES(NEW.node_name,kind,NEW.app_id,first_gap,greatest(first_gap,NEW.updated_at))
 ON CONFLICT(node_name,source_kind,app_id,gap_from) DO UPDATE SET gap_until=greatest(request_analytics_coverage_gaps.gap_until,EXCLUDED.gap_until);
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION record_request_analytics_coverage(node text, boot uuid, counting boolean, unattributed_drops bigint, app_gaps jsonb) RETURNS void LANGUAGE plpgsql AS $$
DECLARE stamp timestamptz:=clock_timestamp(); gap record;
BEGIN
 IF jsonb_typeof(app_gaps) IS DISTINCT FROM 'array' OR unattributed_drops IS NULL OR unattributed_drops<0 OR
  EXISTS(SELECT 1 FROM jsonb_to_recordset(app_gaps) g(app_id uuid,analytics_dropped_count bigint,analytics_pending_count integer)
   WHERE app_id IS NULL OR coalesce(analytics_dropped_count,0)<0 OR coalesce(analytics_pending_count,0)<0) THEN
 RAISE EXCEPTION 'invalid analytics coverage' USING ERRCODE='22023'; END IF;
 INSERT INTO request_analytics_node_coverage AS old(node_name,boot_id,counting_since,heartbeat_at,unattributed_dropped_total)
 VALUES(node,boot,CASE WHEN counting THEN stamp END,stamp,unattributed_drops)
 ON CONFLICT(node_name) DO UPDATE SET boot_id=excluded.boot_id,heartbeat_at=stamp,unattributed_dropped_total=excluded.unattributed_dropped_total,
 counting_since=CASE WHEN NOT counting THEN NULL
  WHEN old.counting_since IS NULL OR old.boot_id<>excluded.boot_id OR old.heartbeat_at<stamp-interval '30 seconds'
   OR excluded.unattributed_dropped_total<>old.unattributed_dropped_total THEN stamp
  ELSE old.counting_since END;
 UPDATE request_analytics_app_gaps SET last_gap_at=stamp,pending_count=0,updated_at=stamp WHERE node_name=node AND pending_count>0;
 FOR gap IN SELECT g.app_id,coalesce(g.analytics_dropped_count,0) AS dropped,coalesce(g.analytics_pending_count,0) AS pending
  FROM jsonb_to_recordset(app_gaps) g(app_id uuid,analytics_dropped_count bigint,analytics_pending_count integer) ORDER BY g.app_id LOOP
 IF (gap.dropped>0 OR gap.pending>0) AND EXISTS(SELECT 1 FROM apps WHERE id=gap.app_id AND status<>'deleted') THEN
 INSERT INTO request_analytics_app_gaps AS old(node_name,app_id,last_gap_at,pending_count,updated_at)
 VALUES(node,gap.app_id,stamp,gap.pending,stamp)
 ON CONFLICT(node_name,app_id) DO UPDATE SET last_gap_at=stamp,pending_count=excluded.pending_count,updated_at=stamp;
 END IF;
 END LOOP;
END $$;

CREATE OR REPLACE FUNCTION record_telemetry_coverage(node text, boot uuid, seq bigint, is_enabled boolean, sampling integer, drops bigint, pending integer, source_stamp timestamptz, scoped boolean, unattributed_drops bigint, app_gaps jsonb) RETURNS void LANGUAGE plpgsql AS $$
DECLARE stamp timestamptz:=clock_timestamp(); attributed_pending bigint; attributed_losses bigint; gap record;
BEGIN
 IF jsonb_typeof(app_gaps) IS DISTINCT FROM 'array' OR (NOT scoped AND jsonb_array_length(app_gaps)>0) THEN
 RAISE EXCEPTION 'invalid app telemetry coverage' USING ERRCODE='22023'; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_to_recordset(app_gaps) g(app_id uuid,dropped_count bigint,pending_count integer) WHERE app_id IS NULL OR app_id='00000000-0000-0000-0000-000000000000'::uuid OR dropped_count IS NULL OR dropped_count<0 OR pending_count IS NULL OR pending_count<0) OR
 EXISTS(SELECT 1 FROM jsonb_to_recordset(app_gaps) g(app_id uuid) GROUP BY app_id HAVING count(*)>1) THEN
 RAISE EXCEPTION 'invalid or duplicated app telemetry gap' USING ERRCODE='22023'; END IF;
 SELECT coalesce(sum(pending_count),0),coalesce(sum(dropped_count),0) INTO attributed_pending,attributed_losses FROM jsonb_to_recordset(app_gaps) g(pending_count integer,dropped_count bigint);
 IF attributed_pending>pending THEN RAISE EXCEPTION 'invalid attributed backlog' USING ERRCODE='22023'; END IF;
 IF source_stamp>stamp+interval '5 seconds' OR source_stamp<stamp-interval '30 seconds' THEN
 RAISE EXCEPTION 'telemetry coverage clock is stale or in the future' USING ERRCODE='22023'; END IF;
 PERFORM 1 FROM apps a WHERE a.id IN (SELECT app_id FROM jsonb_to_recordset(app_gaps) g(app_id uuid)) ORDER BY a.id FOR KEY SHARE;
 INSERT INTO request_telemetry_coverage AS old(node_name,boot_id,sequence,enabled,sampling_basis_points,dropped_total,pending_count,source_at,received_at,healthy_since,app_scoped,unattributed_dropped_total,unattributed_pending_count)
 VALUES(node,boot,seq,is_enabled,sampling,drops,pending,source_stamp,stamp,
 CASE WHEN is_enabled AND sampling=10000 AND (CASE WHEN scoped THEN unattributed_drops=0 AND pending=attributed_pending ELSE drops=0 AND pending=0 END) THEN stamp END,
 scoped,unattributed_drops,(pending-attributed_pending)::integer)
 ON CONFLICT(node_name) DO UPDATE SET boot_id=excluded.boot_id,sequence=excluded.sequence,enabled=excluded.enabled,sampling_basis_points=excluded.sampling_basis_points,
 dropped_total=excluded.dropped_total,pending_count=excluded.pending_count,source_at=excluded.source_at,received_at=excluded.received_at,
 app_scoped=excluded.app_scoped,unattributed_dropped_total=excluded.unattributed_dropped_total,unattributed_pending_count=excluded.unattributed_pending_count,
 healthy_since=CASE WHEN NOT excluded.enabled OR excluded.sampling_basis_points<>10000 OR (CASE WHEN excluded.app_scoped THEN excluded.unattributed_pending_count ELSE excluded.pending_count END)<>0 THEN NULL
 WHEN old.boot_id<>excluded.boot_id OR old.received_at<stamp-interval '30 seconds' OR excluded.dropped_total<old.dropped_total OR
 (excluded.app_scoped AND excluded.dropped_total-old.dropped_total>attributed_losses+greatest(excluded.unattributed_dropped_total-old.unattributed_dropped_total,0)) OR (CASE WHEN excluded.app_scoped THEN old.unattributed_dropped_total<>excluded.unattributed_dropped_total ELSE old.dropped_total<>excluded.dropped_total END) THEN stamp
 ELSE coalesce(old.healthy_since,stamp) END
 WHERE excluded.source_at>old.source_at AND (old.boot_id<>excluded.boot_id OR excluded.sequence>old.sequence);
 IF NOT FOUND THEN RAISE EXCEPTION 'telemetry coverage replay or out-of-order heartbeat' USING ERRCODE='22023'; END IF;
 UPDATE request_telemetry_app_gaps SET last_gap_at=stamp,pending_count=0,updated_at=stamp WHERE node_name=node AND pending_count>0;
 FOR gap IN SELECT * FROM jsonb_to_recordset(app_gaps) g(app_id uuid,dropped_count bigint,pending_count integer) ORDER BY app_id LOOP
 IF (gap.dropped_count>0 OR gap.pending_count>0) AND EXISTS(SELECT 1 FROM apps WHERE id=gap.app_id AND status<>'deleted') THEN
 INSERT INTO request_telemetry_app_gaps AS old(node_name,app_id,last_gap_at,pending_count,dropped_total,updated_at)
 VALUES(node,gap.app_id,stamp,gap.pending_count,gap.dropped_count,stamp)
 ON CONFLICT(node_name,app_id) DO UPDATE SET last_gap_at=stamp,pending_count=excluded.pending_count,dropped_total=old.dropped_total+excluded.dropped_total,updated_at=stamp;
 END IF;
 END LOOP;
END $$;

DROP FUNCTION IF EXISTS record_telemetry_coverage(text, uuid, bigint, boolean, integer, bigint, integer, timestamptz, boolean, bigint, jsonb, boolean);
ALTER TABLE request_analytics_app_gaps DROP CONSTRAINT IF EXISTS request_analytics_app_gaps_pending_since_check;
ALTER TABLE request_analytics_app_gaps DROP COLUMN IF EXISTS pending_since;
ALTER TABLE request_telemetry_app_gaps DROP CONSTRAINT IF EXISTS request_telemetry_app_gaps_pending_since_check;
ALTER TABLE request_telemetry_app_gaps DROP COLUMN IF EXISTS pending_since;
ALTER TABLE request_analytics_app_gaps DROP CONSTRAINT IF EXISTS request_analytics_app_gaps_dropped_total_check;
ALTER TABLE request_analytics_app_gaps DROP COLUMN IF EXISTS dropped_total;
ALTER TABLE request_telemetry_coverage DROP CONSTRAINT IF EXISTS request_telemetry_coverage_clean_stop_at_check;
ALTER TABLE request_telemetry_coverage DROP COLUMN IF EXISTS clean_stop_at;
-- +goose StatementEnd
