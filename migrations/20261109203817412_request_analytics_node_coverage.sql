-- filename: 20261109203817412_request_analytics_node_coverage.sql
-- migration-compat: expand
-- ADR-20261103113952 amendment 6: a gateway that counts its own requests into
-- analytics rollups (amendment 5) reports its analytics losses and late
-- rollups apart from raw telemetry delivery. apid records them here, in the
-- transaction that records the raw coverage heartbeat, and analytics answered
-- by rollups judges such a node by them: a raw row the gateway already
-- counted no longer marks the window incomplete. The raw coverage tables and
-- functions, which route removal relies on, are unchanged. Everything here is
-- new: the previous release neither reads nor writes it. Re-runnable.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS request_analytics_node_coverage (
    node_name text PRIMARY KEY CHECK (node_name <> ''),
    boot_id uuid NOT NULL CHECK (boot_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    counting_since timestamptz,
    heartbeat_at timestamptz NOT NULL,
    unattributed_dropped_total bigint NOT NULL CHECK (unattributed_dropped_total >= 0),
    CHECK (counting_since IS NULL OR counting_since <= heartbeat_at)
);
COMMENT ON TABLE request_analytics_node_coverage IS 'Per gateway node: since when it has counted every request into analytics rollups without a break (ADR-20261103113952 amendment 6); written only by apid.';

CREATE TABLE IF NOT EXISTS request_analytics_app_gaps (
    node_name text NOT NULL CHECK (node_name <> ''),
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    last_gap_at timestamptz NOT NULL,
    pending_count integer NOT NULL CHECK (pending_count >= 0),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (node_name, app_id),
    CHECK (last_gap_at <= updated_at)
);
COMMENT ON TABLE request_analytics_app_gaps IS 'Per gateway node and app: the last analytics loss or late rollup and the requests still late (ADR-20261103113952 amendment 6); written only by apid.';
CREATE INDEX IF NOT EXISTS request_analytics_app_gaps_app_idx ON request_analytics_app_gaps (app_id);

-- +goose StatementBegin
-- record_request_analytics_coverage records one heartbeat's analytics part.
-- counting_since restarts when the node stops counting, reboots, misses
-- heartbeats for 30 s or loses requests it cannot attribute to an app. Each
-- app gap carries analytics_dropped_count (requests lost since the last
-- acknowledged heartbeat) and analytics_pending_count (requests in rollups
-- past their send time and not yet acknowledged); the raw keys of the same
-- objects are ignored here.
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
 -- Omitted apps have nothing late any more.
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

-- request_analytics_counts_window says whether a node's analytics part,
-- recorded with its latest raw heartbeat, shows it counting every request
-- since window_from. A heartbeat an older apid recorded has no analytics part
-- and leaves the node judged by raw delivery.
CREATE OR REPLACE FUNCTION request_analytics_counts_window(node text, window_from timestamptz) RETURNS boolean LANGUAGE sql STABLE AS $f$
 SELECT EXISTS(SELECT 1 FROM request_analytics_node_coverage a JOIN request_telemetry_coverage c ON c.node_name=a.node_name
  WHERE a.node_name=node AND a.boot_id=c.boot_id AND a.counting_since<=window_from AND a.heartbeat_at>=c.received_at)
$f$;

-- request_analytics_completeness_blockers lists why analytics answered by
-- rollups may miss requests of target_app in [window_from, window_until).
-- Every active node must report, fresh, unsampled and caught up to the
-- window's end. A node counting analytics itself over the window is then
-- judged by its analytics gaps; any other node by raw delivery, as
-- route_removal_coverage_blockers judges it.
CREATE OR REPLACE FUNCTION request_analytics_completeness_blockers(target_app uuid, window_from timestamptz, window_until timestamptz) RETURNS jsonb LANGUAGE plpgsql STABLE AS $$
DECLARE blockers jsonb:='[]'; stamp timestamptz:=clock_timestamp();
BEGIN
 IF NOT EXISTS(SELECT 1 FROM compute_nodes WHERE active) OR EXISTS(SELECT 1 FROM compute_nodes n LEFT JOIN request_telemetry_coverage c ON c.node_name=n.name WHERE n.active AND c.node_name IS NULL) THEN
 blockers:=blockers||jsonb_build_array('telemetry_coverage_missing'); END IF;
 IF EXISTS(SELECT 1 FROM request_telemetry_coverage c JOIN compute_nodes n ON n.name=c.node_name WHERE n.active AND c.received_at<stamp-interval '30 seconds') THEN
 blockers:=blockers||jsonb_build_array('telemetry_coverage_stale'); END IF;
 IF EXISTS(SELECT 1 FROM request_telemetry_coverage c JOIN compute_nodes n ON n.name=c.node_name WHERE n.active AND NOT c.enabled) THEN
 blockers:=blockers||jsonb_build_array('telemetry_disabled'); END IF;
 IF EXISTS(SELECT 1 FROM request_telemetry_coverage c JOIN compute_nodes n ON n.name=c.node_name WHERE n.active AND c.sampling_basis_points<>10000) THEN
 blockers:=blockers||jsonb_build_array('telemetry_sampled'); END IF;
 IF EXISTS(SELECT 1 FROM request_telemetry_coverage c JOIN compute_nodes n ON n.name=c.node_name WHERE n.active AND c.source_at<window_until) THEN
 blockers:=blockers||jsonb_build_array('telemetry_ingestion_pending'); END IF;
 -- Nodes judged by raw delivery.
 IF EXISTS(SELECT 1 FROM request_telemetry_coverage c JOIN compute_nodes n ON n.name=c.node_name WHERE n.active AND NOT request_analytics_counts_window(c.node_name,window_from)
  AND (CASE WHEN c.app_scoped THEN c.unattributed_pending_count ELSE c.pending_count END)>0) THEN
 blockers:=blockers||jsonb_build_array('telemetry_ingestion_pending'); END IF;
 IF EXISTS(SELECT 1 FROM request_telemetry_coverage c JOIN compute_nodes n ON n.name=c.node_name WHERE n.active AND NOT request_analytics_counts_window(c.node_name,window_from)
  AND (c.healthy_since IS NULL OR c.healthy_since>window_from)) THEN
 blockers:=blockers||jsonb_build_array('telemetry_window_incomplete'); END IF;
 IF EXISTS(SELECT 1 FROM request_telemetry_app_gaps g JOIN compute_nodes n ON n.name=g.node_name WHERE n.active AND g.app_id=target_app AND g.pending_count>0
  AND NOT request_analytics_counts_window(g.node_name,window_from)) THEN
 blockers:=blockers||jsonb_build_array('telemetry_ingestion_pending'); END IF;
 IF EXISTS(SELECT 1 FROM request_telemetry_app_gaps g JOIN compute_nodes n ON n.name=g.node_name WHERE n.active AND g.app_id=target_app AND g.last_gap_at>window_from
  AND NOT request_analytics_counts_window(g.node_name,window_from)) THEN
 blockers:=blockers||jsonb_build_array('telemetry_window_incomplete'); END IF;
 -- Nodes counting analytics themselves.
 IF EXISTS(SELECT 1 FROM request_analytics_app_gaps g JOIN compute_nodes n ON n.name=g.node_name WHERE n.active AND g.app_id=target_app AND g.pending_count>0
  AND request_analytics_counts_window(g.node_name,window_from)) THEN
 blockers:=blockers||jsonb_build_array('telemetry_ingestion_pending'); END IF;
 IF EXISTS(SELECT 1 FROM request_analytics_app_gaps g JOIN compute_nodes n ON n.name=g.node_name WHERE n.active AND g.app_id=target_app AND g.last_gap_at>window_from
  AND request_analytics_counts_window(g.node_name,window_from)) THEN
 blockers:=blockers||jsonb_build_array('telemetry_window_incomplete'); END IF;
 SELECT coalesce(jsonb_agg(DISTINCT x),'[]') INTO blockers FROM jsonb_array_elements(blockers) x;
 RETURN blockers;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DROP FUNCTION IF EXISTS request_analytics_completeness_blockers(uuid, timestamptz, timestamptz);
DROP FUNCTION IF EXISTS request_analytics_counts_window(text, timestamptz);
DROP FUNCTION IF EXISTS record_request_analytics_coverage(text, uuid, boolean, bigint, jsonb);
-- +goose StatementEnd
DROP TABLE IF EXISTS request_analytics_app_gaps;
DROP TABLE IF EXISTS request_analytics_node_coverage;
