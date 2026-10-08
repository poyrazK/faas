import { setTimeout as delay } from 'node:timers/promises';
import { checkMcpTaskCompatibility, mcpTaskHandlerInventory } from './task-compatibility.js';
import { createMcpTaskAdmissionController } from './task-admission.js';

// Hooks manage deployment processes; database registration alone never stops a worker.
export async function releaseMcpTasks({ pool, namespace, store, handlers, migrate, start, drain, timeoutMs = 60000, checkpoint, saveCheckpoint = async () => {} }) {
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 100 || timeoutMs > 600000 || ![migrate, start, drain].every(fn => typeof fn === 'function')) throw new Error('Invalid release settings');
  const inventory = mcpTaskHandlerInventory(handlers);
  const lock = await pool.connect();
  let locked = false;
  const report = { ok: false, stage: 'release_lock' };
  try {
    const result = await lock.query('SELECT pg_try_advisory_lock(hashtextextended($1, 0)) AS acquired', [`gregale_mcp_release:${namespace}`]);
    if (!result.rows[0].acquired) throw new Error('Release already running');
    locked = true;
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
    if (checkpoint && (checkpoint.namespace !== namespace || !Array.isArray(checkpoint.previousWorkerIDs) || !Number.isFinite(Date.parse(checkpoint.startedAt)))) throw new Error('Invalid release checkpoint');
    const previous = checkpoint?.previousWorkerIDs ?? (await workers()).map(row => row.worker_id);
    const since = checkpoint ? new Date(checkpoint.startedAt) : (await pool.query('SELECT clock_timestamp() AS now')).rows[0].now;
    await saveCheckpoint({ namespace, previousWorkerIDs: previous, startedAt: since.toISOString() });
    report.stage = 'start_replacements';
    const ids = await start({ previousWorkerIDs: previous, timeoutMs });
    if (!Array.isArray(ids) || !ids.length || ids.length > 4096 || new Set(ids).size !== ids.length || ids.some(id => typeof id !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id) || previous.includes(id))) throw new Error('Invalid replacement worker IDs');
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

// Reused by native deployment adapters immediately before promotion and each park.
export async function checkMcpTaskReplacementReadiness({ pool, namespace, handlers, workerIDs }) {
  if (!Array.isArray(workerIDs) || !workerIDs.length || workerIDs.length > 4096 || new Set(workerIDs).size !== workerIDs.length || workerIDs.some(id => typeof id !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(id))) throw new Error('Invalid replacement IDs');
  const inventory = mcpTaskHandlerInventory(handlers);
  const result = await pool.query(`SELECT worker_id::text, handlers FROM gregale_mcp_task_workers
    WHERE namespace = $1 AND worker_id = ANY($2::uuid[]) AND NOT draining
      AND expires_at > clock_timestamp() AND heartbeat_at > clock_timestamp() - interval '45 seconds'`, [namespace, workerIDs]);
  return workerIDs.every(id => result.rows.some(row => row.worker_id === id && inventory.every(handler => row.handlers.some(item => item.name === handler.name && item.version === handler.version))));
}
