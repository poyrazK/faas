// Candidate coverage is checked against retained nonterminal work, independently
// of worker heartbeats, retry eligibility, lease ownership or input pauses.
export function mcpTaskHandlerInventory(handlers) {
  if (!handlers || typeof handlers !== 'object' || Array.isArray(handlers)) throw new Error('Invalid MCP task handler registry');
  const inventory = [];
  for (const [name, handler] of Object.entries(handlers)) {
    if (!/^[a-z][a-z0-9_.-]{0,127}$/.test(name) || !handler || typeof handler.version !== 'string') throw new Error('Invalid MCP task handler registry');
    for (const [version, execute] of Object.entries({ ...handler.previousVersions, [handler.version]: handler.execute })) {
      if (!/^[A-Za-z0-9._-]{1,64}$/.test(version) || typeof execute !== 'function') throw new Error('Invalid MCP task handler registry');
      inventory.push({ name, version });
    }
  }
  return inventory;
}

export async function checkMcpTaskCompatibility({ pool, namespace, handlers }) {
  if (!pool || typeof pool.query !== 'function' || typeof namespace !== 'string' || !namespace.trim()) throw new Error('Invalid MCP task compatibility settings');
  const inventory = mcpTaskHandlerInventory(handlers);
  const result = await pool.query(`
    WITH candidate AS (SELECT name, version FROM jsonb_to_recordset($2::jsonb) AS handler(name text, version text)),
    instant AS MATERIALIZED (SELECT clock_timestamp() AS now)
    SELECT tool_name, handler_version, COUNT(*)::text AS task_count,
           MAX(expires_at) AS latest_expiry,
           COUNT(*) FILTER (WHERE status = 'queued')::text AS queued_count,
           COUNT(*) FILTER (WHERE status = 'running')::text AS running_count,
           COUNT(*) FILTER (WHERE status = 'input_required')::text AS input_required_count
      FROM gregale_mcp_tasks CROSS JOIN instant
     WHERE namespace = $1 AND expires_at > instant.now
       AND status IN ('queued', 'running', 'input_required')
       AND NOT EXISTS (SELECT 1 FROM candidate WHERE candidate.name = tool_name AND candidate.version = handler_version)
     GROUP BY tool_name, handler_version ORDER BY tool_name, handler_version
  `, [namespace, JSON.stringify(inventory)]);
  const gaps = result.rows.map(row => ({
    tool: row.tool_name, version: row.handler_version, taskCount: Number(row.task_count),
    latestExpiry: new Date(row.latest_expiry).toISOString(),
    queuedCount: Number(row.queued_count), runningCount: Number(row.running_count), inputRequiredCount: Number(row.input_required_count),
  }));
  if (gaps.some(gap => !['taskCount', 'queuedCount', 'runningCount', 'inputRequiredCount'].every(key => Number.isSafeInteger(gap[key]) && gap[key] >= 0))) throw new Error('Invalid MCP task compatibility counts');
  return { ok: gaps.length === 0, gaps };
}
