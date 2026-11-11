-- name: LockCustomMetricAdmission :exec
SELECT pg_advisory_xact_lock(hashtextextended('custom-metrics:' || sqlc.arg(app_id)::text::uuid::text, 0));

-- name: PutCustomMetricOfKind :one
WITH admitted AS (
 SELECT 1 WHERE EXISTS (
  SELECT 1 FROM app_custom_metrics WHERE app_id=sqlc.arg(app_id)::uuid AND name=sqlc.arg(name)::text
 ) OR (SELECT count(*) FROM app_custom_metrics WHERE app_id=sqlc.arg(app_id)::uuid) < sqlc.arg(distinct_limit)::integer
), written AS (
 INSERT INTO app_custom_metrics(app_id,name,value,observed_at,kind)
 SELECT sqlc.arg(app_id)::uuid,sqlc.arg(name)::text,sqlc.arg(value)::double precision,sqlc.arg(observed_at)::timestamptz,sqlc.arg(kind)::text
 FROM admitted
 ON CONFLICT(app_id,name) DO UPDATE
 SET value=EXCLUDED.value,observed_at=EXCLUDED.observed_at,kind=EXCLUDED.kind
 WHERE app_custom_metrics.observed_at <= EXCLUDED.observed_at
 OR (app_custom_metrics.observed_at > now()+sqlc.arg(clock_skew_seconds)::integer*interval '1 second' AND EXCLUDED.observed_at<=now()+sqlc.arg(clock_skew_seconds)::integer*interval '1 second')
 RETURNING name
)
SELECT EXISTS(SELECT 1 FROM admitted)::boolean AS admitted;

-- name: ListCustomMetrics :many
SELECT m.name,m.value,m.observed_at,m.kind,EXISTS(SELECT 1 FROM app_custom_metric_series s WHERE s.app_id=m.app_id AND s.name=m.name)::boolean AS labelled FROM app_custom_metrics m WHERE m.app_id=sqlc.arg(app_id)::uuid ORDER BY name;

-- name: DeleteCustomMetric :exec
DELETE FROM app_custom_metrics WHERE app_id=sqlc.arg(app_id)::uuid AND name=sqlc.arg(name)::text;

-- name: ListFreshCustomMetrics :many
SELECT app_id::text AS app_id,name,value FROM app_custom_metrics
WHERE (observed_at >= sqlc.arg(since)::timestamptz OR kind='counter') AND observed_at <= sqlc.arg(until)::timestamptz
ORDER BY app_id,name LIMIT sqlc.arg(row_limit)::integer;

-- name: AddCustomMetricOfKind :one
WITH admitted AS (
 SELECT 1 WHERE EXISTS(SELECT 1 FROM app_custom_metrics WHERE app_id=sqlc.arg(app_id)::uuid AND name=sqlc.arg(name)::text)
 OR (SELECT count(*) FROM app_custom_metrics WHERE app_id=sqlc.arg(app_id)::uuid)<sqlc.arg(distinct_limit)::integer
), written AS (
 INSERT INTO app_custom_metrics(app_id,name,value,observed_at,kind)
 SELECT sqlc.arg(app_id)::uuid,sqlc.arg(name)::text,least(greatest(sqlc.arg(delta)::double precision,0),sqlc.arg(max_total)::double precision),sqlc.arg(observed_at)::timestamptz,sqlc.arg(kind)::text FROM admitted
 ON CONFLICT(app_id,name) DO UPDATE SET
 value=least(greatest(CASE WHEN app_custom_metrics.kind=EXCLUDED.kind THEN app_custom_metrics.value+sqlc.arg(delta)::double precision ELSE sqlc.arg(delta)::double precision END,0),sqlc.arg(max_total)::double precision),
 observed_at=CASE WHEN app_custom_metrics.observed_at>now()+sqlc.arg(clock_skew_seconds)::integer*interval '1 second' AND EXCLUDED.observed_at<=now()+sqlc.arg(clock_skew_seconds)::integer*interval '1 second' THEN EXCLUDED.observed_at ELSE greatest(app_custom_metrics.observed_at,EXCLUDED.observed_at) END,
 kind=EXCLUDED.kind RETURNING name
)
SELECT EXISTS(SELECT 1 FROM admitted)::boolean AS admitted;
