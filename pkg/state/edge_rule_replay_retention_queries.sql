-- name: PruneEdgeRuleReplayHistory :execrows
-- ADR-570: the replay floor is each serving gateway's fresh, boot-scoped
-- observation; a stale or missing gateway keeps the history it may replay.
WITH db_clock AS MATERIALIZED (SELECT clock_timestamp() AS now)
DELETE FROM edge_rule_change_log AS old
WHERE created_at < sqlc.arg(before)::timestamptz
  -- Preserve each account's cursor and the unknown-owner compatibility floor.
  AND (old.account_ids IS NOT NULL OR old.id < (SELECT MAX(id) FROM edge_rule_change_log WHERE account_ids IS NULL))
  AND NOT EXISTS (
    SELECT 1 FROM unnest(old.account_ids) AS owner(account_id)
    WHERE NOT EXISTS (
      SELECT 1 FROM edge_rule_change_log AS newer
      WHERE newer.id > old.id AND newer.account_ids @> ARRAY[owner.account_id]
    )
  )
  AND old.id <= COALESCE((
      SELECT MIN(CASE WHEN o.reported_at BETWEEN db_clock.now - make_interval(secs => sqlc.arg(freshness_seconds)::double precision) AND db_clock.now
                       AND p.observed_at BETWEEN db_clock.now - make_interval(secs => sqlc.arg(freshness_seconds)::double precision) AND db_clock.now
                      THEN p.last_change_id ELSE 0 END)
      FROM compute_nodes n CROSS JOIN db_clock
      LEFT JOIN gateway_traffic_runtime_observations o ON o.node_name = n.name
      LEFT JOIN gateway_traffic_policy_observations p ON p.node_name = n.name AND p.policy_kind = 'edge_rules'
          AND p.generation = o.generation AND p.boot_id = o.boot_id
      WHERE n.active AND n.role IN ('compute-only', 'compute-node')
        AND n.gateway_target_url IS NOT NULL AND btrim(n.gateway_target_url) <> ''
  ), 9223372036854775807::bigint);

-- name: ReadAccountEdgeRulePolicyPosition :one
SELECT GREATEST(
    COALESCE((SELECT MAX(id) FROM edge_rule_change_log WHERE account_ids @> ARRAY[sqlc.arg(account_id)::uuid]), 0),
    COALESCE((SELECT MAX(id) FROM edge_rule_change_log WHERE account_ids IS NULL), 0)
)::bigint AS desired_revision,
COALESCE((SELECT MAX(id) FROM edge_rule_change_log WHERE account_ids IS NULL), 0)::bigint AS legacy_fleet_revision;
