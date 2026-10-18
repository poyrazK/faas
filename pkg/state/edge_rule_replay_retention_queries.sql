-- name: PruneEdgeRuleReplayHistory :execrows
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
  AND id <= COALESCE((
      SELECT MIN(COALESCE(w.last_change_id, 0))
      FROM compute_nodes n
      LEFT JOIN gateway_edge_rule_watermarks w ON w.node_name = n.name
      WHERE n.active = true
        AND n.role IN ('compute-only', 'compute-node')
        AND n.gateway_target_url IS NOT NULL
        AND btrim(n.gateway_target_url) <> ''
  ), 9223372036854775807::bigint);

-- name: ReadAccountEdgeRulePolicyPosition :one
SELECT GREATEST(
    COALESCE((SELECT MAX(id) FROM edge_rule_change_log WHERE account_ids @> ARRAY[sqlc.arg(account_id)::uuid]), 0),
    COALESCE((SELECT MAX(id) FROM edge_rule_change_log WHERE account_ids IS NULL), 0)
)::bigint AS desired_revision,
COALESCE((SELECT MAX(id) FROM edge_rule_change_log WHERE account_ids IS NULL), 0)::bigint AS legacy_fleet_revision;
