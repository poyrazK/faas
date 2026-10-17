// Activity retention (ADR-1190). GoTrue never prunes its audit log, so with
// AUTH_AUDIT_RETENTION_DAYS (validated in config.mjs) the front deletes older
// entries in batches. Like
// lifecycle events it runs after requests and at startup, at most once per
// interval, never on a timer: an idle app neither wakes nor keeps its
// database awake.
import { limits } from './config.mjs'

export function createAuditPruner({ pool, days, now = Date.now, log = console.error }) {
  let last = -Infinity, running = null
  async function prune() {
    let deleted = 0
    for (let batch = 0; batch < limits.auditPruneBatches; batch++) {
      const { rowCount } = await pool.query(`DELETE FROM auth.audit_log_entries WHERE id IN (
          SELECT id FROM auth.audit_log_entries WHERE created_at < now() - make_interval(days => $1) LIMIT $2)`, [days, limits.auditPruneBatch])
      deleted += rowCount
      if (rowCount < limits.auditPruneBatch) break
    }
    if (deleted) log(JSON.stringify({ level: 'info', event: 'audit_log_pruned', count: deleted }))
  }
  return {
    poke() {
      if (!days || running || now() - last < limits.auditPruneIntervalSeconds * 1000) return running
      last = now()
      running = prune().catch(err => log(JSON.stringify({ level: 'error', event: 'audit_log_prune_failed', sqlstate: err?.code ?? null }))).finally(() => { running = null })
      return running
    },
  }
}
