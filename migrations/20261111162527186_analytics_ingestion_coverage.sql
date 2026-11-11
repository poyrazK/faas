-- filename: 20261111162527186_analytics_ingestion_coverage.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- Pending rollup batches are registered before acknowledgement and removed
-- in the transaction that commits their counts. A crashed writer leaves
-- durable evidence of its missing window. Lost windows are never cleared.
CREATE FUNCTION request_analytics_method(method text) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT CASE WHEN method IN ('GET','POST','PUT','PATCH','DELETE','HEAD','OPTIONS','OTHER') THEN method ELSE 'OTHER' END
$$;

CREATE TABLE request_analytics_rollup_audit (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 audited_since timestamptz NOT NULL
);

CREATE TABLE request_analytics_rollup_windows (
 id uuid PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 bucket_start timestamptz NOT NULL,
 state text NOT NULL CHECK(state IN ('pending','lost'))
);
CREATE INDEX request_analytics_rollup_windows_retention_idx ON request_analytics_rollup_windows(bucket_start);
CREATE INDEX request_analytics_rollup_windows_app_idx ON request_analytics_rollup_windows(app_id,bucket_start);

-- Preserve positive delivery evidence across later restarts or outages.
-- The latest heartbeat alone cannot describe a completed historical window.
CREATE TABLE request_analytics_coverage_watermarks (
 node_name text NOT NULL CHECK(node_name<>''),
 source_kind text NOT NULL DEFAULT 'raw' CHECK(source_kind IN ('raw','analytics')),
 PRIMARY KEY(node_name,source_kind),
 registered_at timestamptz NOT NULL,
 previous_source_at timestamptz NOT NULL,
 source_at timestamptz NOT NULL CHECK(source_at>=previous_source_at)
);
CREATE TABLE request_analytics_coverage_intervals (
 node_name text NOT NULL CHECK(node_name<>''),
 source_kind text NOT NULL DEFAULT 'raw' CHECK(source_kind IN ('raw','analytics')),
 covered_since timestamptz NOT NULL,
 covered_until timestamptz NOT NULL CHECK(covered_until>=covered_since),
 PRIMARY KEY(node_name,source_kind,covered_since)
);
CREATE INDEX request_analytics_coverage_intervals_retention_idx ON request_analytics_coverage_intervals(covered_until);
CREATE TABLE request_analytics_coverage_gaps (
 node_name text NOT NULL CHECK(node_name<>''),
 source_kind text NOT NULL DEFAULT 'raw' CHECK(source_kind IN ('raw','analytics')),
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 gap_from timestamptz NOT NULL,
 gap_until timestamptz NOT NULL CHECK(gap_until>=gap_from),
 history_incomplete boolean NOT NULL DEFAULT false,
 PRIMARY KEY(node_name,source_kind,app_id,gap_from)
);

CREATE INDEX request_analytics_coverage_gaps_app_idx ON request_analytics_coverage_gaps(app_id,gap_from,gap_until);
CREATE INDEX request_analytics_coverage_gaps_retention_idx ON request_analytics_coverage_gaps(gap_until);
CREATE FUNCTION request_analytics_record_coverage_interval() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE previous_stamp timestamptz:=NEW.source_at;
BEGIN
 IF TG_OP='UPDATE' THEN previous_stamp:=least(OLD.source_at,NEW.source_at); END IF;
 INSERT INTO request_analytics_coverage_watermarks(node_name,registered_at,previous_source_at,source_at)
 VALUES(NEW.node_name,coalesce((SELECT created_at FROM compute_nodes WHERE name=NEW.node_name),previous_stamp),previous_stamp,NEW.source_at)
 ON CONFLICT(node_name,source_kind) DO UPDATE SET previous_source_at=EXCLUDED.previous_source_at,source_at=EXCLUDED.source_at;
 IF NEW.healthy_since IS NOT NULL AND NEW.source_at>=NEW.healthy_since THEN
  INSERT INTO request_analytics_coverage_intervals(node_name,covered_since,covered_until)
  VALUES(NEW.node_name,NEW.healthy_since,NEW.source_at)
  ON CONFLICT(node_name,source_kind,covered_since) DO UPDATE SET covered_until=greatest(request_analytics_coverage_intervals.covered_until,EXCLUDED.covered_until);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER request_analytics_record_coverage_interval AFTER INSERT OR UPDATE ON request_telemetry_coverage
 FOR EACH ROW EXECUTE FUNCTION request_analytics_record_coverage_interval();

CREATE FUNCTION request_analytics_record_coverage_gap() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER request_analytics_record_coverage_gap AFTER INSERT OR UPDATE ON request_telemetry_app_gaps
 FOR EACH ROW EXECUTE FUNCTION request_analytics_record_coverage_gap();

INSERT INTO request_analytics_coverage_watermarks(node_name,registered_at,previous_source_at,source_at)
 SELECT c.node_name,n.created_at,c.source_at,c.source_at FROM request_telemetry_coverage c JOIN compute_nodes n ON n.name=c.node_name;
INSERT INTO request_analytics_coverage_intervals(node_name,covered_since,covered_until)
 SELECT node_name,healthy_since,source_at FROM request_telemetry_coverage WHERE healthy_since IS NOT NULL AND source_at>=healthy_since;
INSERT INTO request_analytics_coverage_gaps(node_name,app_id,gap_from,gap_until,history_incomplete)
 SELECT node_name,app_id,last_gap_at,greatest(last_gap_at,updated_at),true FROM request_telemetry_app_gaps;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='faas_runtime') THEN
  GRANT SELECT,INSERT,UPDATE,DELETE ON request_analytics_rollup_audit,request_analytics_rollup_windows,request_analytics_coverage_intervals,request_analytics_coverage_gaps,request_analytics_coverage_watermarks TO faas_runtime;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER request_analytics_record_coverage_gap ON request_telemetry_app_gaps;
DROP TRIGGER request_analytics_record_coverage_interval ON request_telemetry_coverage;
DROP FUNCTION request_analytics_record_coverage_gap();
DROP FUNCTION request_analytics_record_coverage_interval();
DROP TABLE request_analytics_coverage_gaps;
DROP TABLE request_analytics_coverage_intervals;
DROP TABLE request_analytics_coverage_watermarks;
DROP TABLE request_analytics_rollup_windows;
DROP TABLE request_analytics_rollup_audit;
DROP FUNCTION request_analytics_method(text);
