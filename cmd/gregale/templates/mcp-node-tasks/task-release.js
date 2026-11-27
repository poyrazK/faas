import { setTimeout as delay } from 'node:timers/promises';
import { randomUUID } from 'node:crypto';
import { checkMcpTaskCompatibility, mcpTaskHandlerInventory } from './task-compatibility.js';
import { createMcpTaskAdmissionController } from './task-admission.js';

// Hooks manage deployment processes; database registration alone never stops a worker.
export async function releaseMcpTasks({ pool, namespace, store, handlers, migrate, start, drain, timeoutMs = 60000, releaseID, checkpoint, saveCheckpoint = async () => {} }) {
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 100 || timeoutMs > 600000 || ![migrate, start, drain].every(fn => typeof fn === 'function')) throw new Error('Invalid release settings');
  const inventory = mcpTaskHandlerInventory(handlers);
  const requestedID = releaseID ?? checkpoint?.releaseID ?? randomUUID();
  const lock = await pool.connect();
  let locked = false;
  const report = { ok: false, stage: 'release_lock' };
  try {
    const result = await lock.query('SELECT pg_try_advisory_lock(hashtextextended($1, 0)) AS acquired', [`gregale_mcp_release:${namespace}`]);
    if (!result.rows[0].acquired) throw new Error('Release already running');
    locked = true;
    report.stage = 'checkpoint';
    if (typeof requestedID !== 'string' || !/^[A-Za-z0-9._:-]{1,128}$/.test(requestedID)) throw new Error('Invalid release ID');
    if (checkpoint) {
      validateReleaseCheckpoint(checkpoint, namespace);
      if (checkpoint.releaseID !== requestedID) {
        if (!checkpoint.completed) throw new Error('Finish the pending release before starting another');
        checkpoint = undefined;
      }
    }
    report.stage = 'migrations';
    await migrate();
    report.stage = 'candidate_preflight';
    const preflight = async () => {
      await store.initialize({ admissionHandlers: Object.entries(handlers).map(([name, handler]) => ({ name, version: handler.version })) });
      if (!(await checkMcpTaskCompatibility({ pool, namespace, handlers })).ok) throw new Error('Candidate lacks retained handlers');
      const admission = await createMcpTaskAdmissionController({ pool, namespace }).status();
      // Every still-admitted version must remain supported, including versions with no Tasks yet.
      if (!admission.enforced || admission.versions.some(entry => entry.state === 'allowed' && !inventory.some(handler => handler.name === entry.tool && handler.version === entry.version))) throw new Error('Candidate lacks admitted handlers');
      if (!Object.entries(handlers).every(([name, handler]) => admission.versions.some(entry => entry.tool === name && entry.version === handler.version && entry.state === 'allowed'))) throw new Error('Candidate admission disabled');
    };
    await preflight();
    const workers = async () => (await pool.query(`SELECT worker_id::text, handlers, draining, heartbeat_at, heartbeat_at > clock_timestamp() - interval '45 seconds' AS fresh FROM gregale_mcp_task_workers WHERE namespace = $1 AND expires_at > clock_timestamp()`, [namespace])).rows;
    const previous = checkpoint?.previousWorkerIDs ?? (await workers()).map(row => row.worker_id);
    const since = checkpoint ? new Date(checkpoint.startedAt) : (await pool.query('SELECT clock_timestamp() AS now')).rows[0].now;
    const record = { version: 1, namespace, releaseID: requestedID, previousWorkerIDs: previous, startedAt: since.toISOString() };
    if (!checkpoint) await saveCheckpoint(record);
    report.stage = 'start_replacements';
    const ids = checkpoint?.replacementWorkerIDs ?? await start({ releaseID: requestedID, previousWorkerIDs: previous, timeoutMs });
    if (!validReleaseWorkerIDs(ids) || ids.some(id => previous.includes(id))) throw new Error('Invalid replacement worker IDs');
    if (!checkpoint?.replacementWorkerIDs) await saveCheckpoint({ ...record, replacementWorkerIDs: ids });
    report.stage = 'replacement_readiness';
    const ready = async () => {
      const rows = await workers();
      return ids.every(id => rows.some(row => row.worker_id === id && !row.draining && new Date(row.heartbeat_at) >= since && row.fresh && inventory.every(handler => row.handlers.some(item => item.name === handler.name && item.version === handler.version))));
    };
    const wait = async check => {
      const deadline = Date.now() + timeoutMs;
      while (!await check()) {
        if (Date.now() >= deadline) throw new Error('Release deadline exceeded');
        await delay(Math.min(250, Math.max(1, deadline - Date.now())));
      }
    };
    if (checkpoint?.completed) {
      if (!await ready() || (await workers()).some(row => previous.includes(row.worker_id))) throw new Error('Completed release readiness lost');
      return { ok: true, stage: 'complete', replacementWorkerIDs: ids };
    }
    await wait(ready);
    await preflight();
    report.stage = 'drain_previous';
    await drain({ previousWorkerIDs: previous, replacementWorkerIDs: ids, timeoutMs });
    await wait(async () => {
      if (!await ready()) throw new Error('Replacement readiness lost');
      return !(await workers()).some(row => previous.includes(row.worker_id));
    });
    await preflight();
    if (!await ready()) throw new Error('Replacement readiness lost');
    report.stage = 'checkpoint_complete';
    await saveCheckpoint({ ...record, replacementWorkerIDs: ids, completed: true });
    report.ok = true;
    report.stage = 'complete';
    report.replacementWorkerIDs = ids;
    return report;
  } catch {
    // Hooks and database errors can contain credentials. Return only the failing stage.
    return report;
  } finally {
    try {
      if (locked) await lock.query('SELECT pg_advisory_unlock(hashtextextended($1, 0))', [`gregale_mcp_release:${namespace}`]);
    } finally { lock.release(); }
  }
}

function validReleaseWorkerIDs(ids, allowEmpty = false) {
  return Array.isArray(ids) && (allowEmpty || ids.length > 0) && ids.length <= 4096
    && new Set(ids).size === ids.length && ids.every(id => typeof id === 'string'
      && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(id));
}

function validateReleaseCheckpoint(checkpoint, namespace) {
  if (checkpoint.version !== 1 || checkpoint.namespace !== namespace
      || typeof checkpoint.releaseID !== 'string' || !/^[A-Za-z0-9._:-]{1,128}$/.test(checkpoint.releaseID)
      || !validReleaseWorkerIDs(checkpoint.previousWorkerIDs, true)
      || typeof checkpoint.startedAt !== 'string' || !Number.isFinite(Date.parse(checkpoint.startedAt))
      || (checkpoint.completed !== undefined && typeof checkpoint.completed !== 'boolean')
      || (checkpoint.completed && !checkpoint.replacementWorkerIDs)
      || (checkpoint.replacementWorkerIDs !== undefined && (!validReleaseWorkerIDs(checkpoint.replacementWorkerIDs)
        || checkpoint.replacementWorkerIDs.some(id => checkpoint.previousWorkerIDs.includes(id))))) {
    throw new Error('Invalid or legacy release checkpoint; reconcile it before proceeding');
  }
}

// Reused by native deployment adapters immediately before promotion and each park.
export async function checkMcpTaskReplacementReadiness({ pool, namespace, handlers, workerIDs }) {
  if (typeof namespace !== 'string' || !namespace.trim() || !Array.isArray(workerIDs) || !workerIDs.length || workerIDs.length > 4096 || new Set(workerIDs).size !== workerIDs.length || workerIDs.some(id => typeof id !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(id))) throw new Error('Invalid replacement IDs');
  const inventory = mcpTaskHandlerInventory(handlers);
  const result = await pool.query(`SELECT worker_id::text, handlers FROM gregale_mcp_task_workers
    WHERE namespace = $1 AND worker_id = ANY($2::uuid[]) AND NOT draining
      AND expires_at > clock_timestamp() AND heartbeat_at > clock_timestamp() - interval '45 seconds'`, [namespace, workerIDs]);
  return workerIDs.every(id => result.rows.some(row => row.worker_id === id && inventory.every(handler => row.handlers.some(item => item.name === handler.name && item.version === handler.version))));
}

// Restoration shares the release lock and makes no schema or admission changes.
export async function restoreMcpTasks({ pool, namespace, handlers, restore }) {
  const inventory = mcpTaskHandlerInventory(handlers);
  const lock = await pool.connect();
  let locked = false;
  const report = { ok: false, stage: 'release_lock' };
  try {
    const result = await lock.query('SELECT pg_try_advisory_lock(hashtextextended($1, 0)) AS acquired', [`gregale_mcp_release:${namespace}`]);
    if (!result.rows[0].acquired) throw new Error('Release already running');
    locked = true;
    report.stage = 'restore_compatibility';
    if (!(await checkMcpTaskCompatibility({ pool, namespace, handlers })).ok) throw new Error('Retained handlers incompatible');
    const admission = await createMcpTaskAdmissionController({ pool, namespace }).status();
    if (!admission.enforced || admission.versions.some(entry => entry.state === 'allowed' && !inventory.some(handler => handler.name === entry.tool && handler.version === entry.version))) throw new Error('Admitted handlers incompatible');
    report.stage = 'restore_web';
    await restore();
    report.stage = 'web_restored';
    report.ok = true;
  } catch {
    // Keep credentials and child diagnostics out of the report.
  } finally {
    try {
      if (locked) await lock.query('SELECT pg_advisory_unlock(hashtextextended($1, 0))', [`gregale_mcp_release:${namespace}`]);
    } finally { lock.release(); }
  }
  return report;
}

// Share the claim transaction lock: once this commits, updated runtimes cannot
// acquire another lease. Existing handlers keep their leases until graceful stop.
export async function quarantineMcpTaskWorkers({ pool, namespace, workerIDs }) {
  if (typeof namespace !== 'string' || !namespace.trim() || !Array.isArray(workerIDs) || !workerIDs.length || workerIDs.length > 4096 || new Set(workerIDs).size !== workerIDs.length || workerIDs.some(id => typeof id !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(id))) throw new Error('Invalid quarantine IDs');
  const client = await pool.connect();
  try {
    await client.query('BEGIN');
    await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))', [`gregale_mcp_tasks:execution:${namespace}`]);
    await client.query(`INSERT INTO gregale_mcp_task_workers (namespace, worker_id, handlers, draining, heartbeat_at, expires_at)
      SELECT $1, worker_id, '[]'::jsonb, true, clock_timestamp(), clock_timestamp() FROM unnest($2::uuid[]) AS ids(worker_id)
      ON CONFLICT (namespace, worker_id) DO UPDATE SET draining=true`, [namespace, workerIDs]);
    await client.query('COMMIT');
  } catch (error) { await client.query('ROLLBACK'); throw error; }
  finally { client.release(); }
}
