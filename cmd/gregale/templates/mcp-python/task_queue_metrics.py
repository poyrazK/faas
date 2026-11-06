"""Read-only task gauges with the same queue states as the Node observer."""

TASK_QUEUE_METRICS_SQL = """
WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS now),
retained AS MATERIALIZED (
 SELECT COALESCE((SELECT owner_group FROM gregale_mcp_task_owners WHERE namespace=task.namespace AND owner_hash=task.owner_hash),''::bytea) AS owner_group,
        task.status,task.tool_name,task.handler_version,task.created_at,task.next_attempt_at,task.attempt_count,task.resume_pending,instant.now,
        status='running' AND lease_expires_at>instant.now AS live
 FROM gregale_mcp_tasks AS task CROSS JOIN instant
 WHERE namespace=%(namespace)s AND expires_at>instant.now AND status IN ('queued','running','failed')
), registrations AS MATERIALIZED (
 SELECT handlers,draining FROM gregale_mcp_task_workers CROSS JOIN instant WHERE namespace=%(namespace)s AND expires_at>instant.now
), workers AS MATERIALIZED (SELECT handlers FROM registrations WHERE NOT draining),
supported AS MATERIALIZED (
 SELECT DISTINCT handler.name,handler.version FROM workers
 CROSS JOIN LATERAL jsonb_to_recordset(workers.handlers) AS handler(name text,version text)
), pending AS MATERIALIZED (SELECT * FROM retained WHERE status<>'failed'),
owners AS (SELECT owner_group,COUNT(*) FILTER (WHERE live) AS running FROM pending GROUP BY owner_group),
totals AS (SELECT COUNT(*) FILTER (WHERE live) AS running FROM pending)
SELECT LEAST(COUNT(*),1000000000000::bigint)::bigint AS outstanding_tasks,
 LEAST(COALESCE(GREATEST(EXTRACT(EPOCH FROM ((SELECT now FROM instant)-MIN(created_at))),0),0),1000000000000)::double precision AS oldest_age_seconds,
 LEAST(COUNT(*) FILTER (WHERE live),1000000000000::bigint)::bigint AS running_tasks,
 LEAST(COUNT(*) FILTER (WHERE NOT live AND (next_attempt_at IS NULL OR next_attempt_at<=now) AND (totals.running>=%(max_running)s OR owners.running>=%(max_running_per_owner)s)),1000000000000::bigint)::bigint AS capacity_waiting_tasks,
 LEAST((SELECT COUNT(*) FROM retained WHERE status='failed'),1000000000000::bigint)::bigint AS failed_tasks,
 LEAST(COUNT(*) FILTER (WHERE status='queued' AND next_attempt_at>now),1000000000000::bigint)::bigint AS retry_waiting_tasks,
 LEAST((SELECT COUNT(*) FROM workers),1000000000000::bigint)::bigint AS active_workers,
 LEAST((SELECT COUNT(*) FROM registrations WHERE draining),1000000000000::bigint)::bigint AS draining_workers,
 LEAST(COUNT(*) FILTER (WHERE NOT live AND (next_attempt_at IS NULL OR next_attempt_at<=now)
   AND (attempt_count<%(max_attempts)s OR (status='queued' AND resume_pending)) AND EXISTS (SELECT 1 FROM workers)
   AND NOT EXISTS (SELECT 1 FROM supported WHERE supported.name=pending.tool_name AND supported.version=pending.handler_version)),1000000000000::bigint)::bigint AS unsupported_handler_tasks,
 (SELECT LEAST(COUNT(*),1000000000000::bigint)::bigint FROM gregale_mcp_tasks AS queued CROSS JOIN instant
   WHERE queued.namespace=%(namespace)s AND queued.expires_at>instant.now
     AND ((queued.status='queued' AND (queued.next_attempt_at IS NULL OR queued.next_attempt_at<=instant.now)) OR (queued.status='running' AND queued.lease_expires_at<=instant.now))
     AND (queued.attempt_count<%(max_attempts)s OR (queued.status='queued' AND queued.resume_pending))
     AND NOT EXISTS (SELECT 1 FROM gregale_mcp_task_worker_handlers AS handler WHERE handler.namespace=queued.namespace
       AND handler.tool_name=queued.tool_name AND handler.handler_version=queued.handler_version
       AND handler.heartbeat_at>instant.now-interval '45 seconds')) AS without_compatible_worker_tasks
FROM pending JOIN owners USING (owner_group) CROSS JOIN totals
"""
