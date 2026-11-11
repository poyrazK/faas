-- filename: 20261111163357221_analytics_counting_coverage_history.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE FUNCTION request_analytics_record_counting_interval() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE previous_stamp timestamptz:=NEW.heartbeat_at; until_stamp timestamptz;
BEGIN
 IF TG_OP='UPDATE' THEN previous_stamp:=least(OLD.heartbeat_at,NEW.heartbeat_at); END IF;
 INSERT INTO request_analytics_coverage_watermarks(node_name,source_kind,registered_at,previous_source_at,source_at)
 VALUES(NEW.node_name,'analytics',coalesce((SELECT created_at FROM compute_nodes WHERE name=NEW.node_name),previous_stamp),previous_stamp,NEW.heartbeat_at)
 ON CONFLICT(node_name,source_kind) DO UPDATE SET previous_source_at=EXCLUDED.previous_source_at,source_at=EXCLUDED.source_at;
 SELECT least(NEW.heartbeat_at,c.source_at) INTO until_stamp FROM request_telemetry_coverage c
 WHERE c.node_name=NEW.node_name AND c.boot_id=NEW.boot_id AND c.enabled AND c.sampling_basis_points=10000;
 IF NEW.counting_since IS NOT NULL AND until_stamp>=NEW.counting_since THEN
 INSERT INTO request_analytics_coverage_intervals(node_name,source_kind,covered_since,covered_until)
 VALUES(NEW.node_name,'analytics',NEW.counting_since,until_stamp)
 ON CONFLICT(node_name,source_kind,covered_since) DO UPDATE SET covered_until=greatest(request_analytics_coverage_intervals.covered_until,EXCLUDED.covered_until);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER request_analytics_record_counting_interval AFTER INSERT OR UPDATE ON request_analytics_node_coverage
 FOR EACH ROW EXECUTE FUNCTION request_analytics_record_counting_interval();
CREATE TRIGGER request_analytics_record_counting_gap AFTER INSERT OR UPDATE ON request_analytics_app_gaps
 FOR EACH ROW EXECUTE FUNCTION request_analytics_record_coverage_gap();

INSERT INTO request_analytics_coverage_watermarks(node_name,source_kind,registered_at,previous_source_at,source_at)
 SELECT c.node_name,'analytics',n.created_at,c.heartbeat_at,c.heartbeat_at FROM request_analytics_node_coverage c JOIN compute_nodes n ON n.name=c.node_name;
INSERT INTO request_analytics_coverage_intervals(node_name,source_kind,covered_since,covered_until)
 SELECT a.node_name,'analytics',a.counting_since,least(a.heartbeat_at,c.source_at)
 FROM request_analytics_node_coverage a JOIN request_telemetry_coverage c ON c.node_name=a.node_name AND c.boot_id=a.boot_id
 WHERE a.counting_since IS NOT NULL AND c.enabled AND c.sampling_basis_points=10000 AND least(a.heartbeat_at,c.source_at)>=a.counting_since;
INSERT INTO request_analytics_coverage_gaps(node_name,source_kind,app_id,gap_from,gap_until,history_incomplete)
 SELECT node_name,'analytics',app_id,last_gap_at,greatest(last_gap_at,updated_at),true FROM request_analytics_app_gaps;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER request_analytics_record_counting_gap ON request_analytics_app_gaps;
DROP TRIGGER request_analytics_record_counting_interval ON request_analytics_node_coverage;
DROP FUNCTION request_analytics_record_counting_interval();
