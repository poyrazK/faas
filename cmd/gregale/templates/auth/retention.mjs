// Activity and anonymous-user retention (ADR-1190). GoTrue prunes neither
// its audit log nor anonymous users, so with AUTH_AUDIT_RETENTION_DAYS and
// AUTH_ANONYMOUS_RETENTION_DAYS (validated in config.mjs) the front deletes
// older entries and idle anonymous users in batches. Like
// lifecycle events it runs after requests and at startup, at most once per
// interval, never on a timer: an idle app neither wakes nor keeps its
// database awake.
import { limits } from './config.mjs'

const auditStatement = `DELETE FROM auth.audit_log_entries WHERE id IN (
    SELECT id FROM auth.audit_log_entries WHERE created_at < now() - make_interval(days => $1) LIMIT $2)`

// An anonymous user is idle when it was created, and none of its sessions
// was refreshed, within the retention. Deleting the user ends its sessions,
// identities and memberships; tables of the application that reference
// auth.users decide for themselves (ON DELETE CASCADE or not).
const anonymousStatement = `DELETE FROM auth.users WHERE id IN (
    SELECT u.id FROM auth.users u
    WHERE u.is_anonymous AND u.created_at < now() - make_interval(days => $1)
      AND NOT EXISTS (SELECT 1 FROM auth.sessions s WHERE s.user_id = u.id
        AND greatest(s.created_at, s.updated_at, s.refreshed_at AT TIME ZONE 'UTC') > now() - make_interval(days => $1))
    LIMIT $2)`

export function createAuditPruner(options) {
  return createPruner({ ...options, statement: auditStatement, event: 'audit_log' })
}

export function createAnonymousPruner(options) {
  return createPruner({ ...options, statement: anonymousStatement, event: 'anonymous_users' })
}

function createPruner({ pool, days, statement, event, now = Date.now, log = console.error }) {
  let last = -Infinity, running = null
  async function prune() {
    let deleted = 0
    for (let batch = 0; batch < limits.auditPruneBatches; batch++) {
      const { rowCount } = await pool.query(statement, [days, limits.auditPruneBatch])
      deleted += rowCount
      if (rowCount < limits.auditPruneBatch) break
    }
    if (deleted) log(JSON.stringify({ level: 'info', event: `${event}_pruned`, count: deleted }))
  }
  return {
    poke() {
      if (!days || running || now() - last < limits.auditPruneIntervalSeconds * 1000) return running
      last = now()
      running = prune().catch(err => log(JSON.stringify({ level: 'error', event: `${event}_prune_failed`, sqlstate: err?.code ?? null }))).finally(() => { running = null })
      return running
    },
  }
}
