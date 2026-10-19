import {captureReadinessProbe as probe} from './data-repair-readiness-probe.js';
import type {OperationConnection, OperationPool} from './operation-receipt.js';
import type {DataRepairCapture} from './data-repair.js';

export type DataRepairReadinessStatus = 'ready' | 'blocked' | 'unknown';
export interface DataRepairCaptureReadinessOptions extends DataRepairCapture {
  accountId: string; appId: string; platformTenantId?: string; receiptProtocol?: 'managed' | 'customer';
}
export interface DataRepairReadinessFinding {
  id: string; code: string; status: DataRepairReadinessStatus; summary: string; action: string; objects: string[];
}
export interface DataRepairCaptureReadinessReport {
  version: number; profile: 'capture'; scope: {account_id: string; app_id: string; environment: string; platform_tenant_id?: string};
  table: string; release: string; receipt_protocol: 'managed' | 'customer'; checked_at: string; status: DataRepairReadinessStatus; findings: DataRepairReadinessFinding[];
}

function options(input: DataRepairCaptureReadinessOptions) {
  const uuid = (v: unknown) => typeof v === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(v) && v !== '00000000-0000-0000-0000-000000000000';
  const context = (v: unknown) => typeof v === 'string' && v.length > 0 && Buffer.byteLength(v) <= probe.max_context_bytes && !v.includes('\0') && Buffer.from(v).toString() === v;
  const receipt = input.receiptProtocol ?? 'managed';
  if (!uuid(input.accountId) || !uuid(input.appId) || (input.platformTenantId !== undefined && input.platformTenantId !== '' && !uuid(input.platformTenantId)) ||
    !context(input.environment) || !context(input.release) || typeof input.table !== 'string' || input.table.trim() !== input.table || !input.table.includes('.') ||
    input.table.includes('\0') || Buffer.from(input.table).toString() !== input.table || Buffer.byteLength(input.table) > probe.max_table_bytes ||
    !['managed', 'customer'].includes(receipt) || receipt === 'customer' && !input.platformTenantId) throw new TypeError('invalid data repair capture readiness options');
  return {scope: {account_id: input.accountId, app_id: input.appId, environment: input.environment, ...(input.platformTenantId ? {platform_tenant_id: input.platformTenantId} : {})},
    table: input.table, release: input.release, receipt_protocol: receipt};
}

function finding(id: string, code: string, objects: string[] = []): DataRepairReadinessFinding {
  const m = probe.messages[code];
  if (!m || (m.id && m.id !== id) || !probe.finding_ids.includes(id) || objects.length > probe.max_objects ||
    objects.some(o => typeof o !== 'string' || !o || Buffer.byteLength(o) > probe.max_table_bytes || o.includes('\0'))) throw new Error('invalid capture probe frame');
  return {id, code, status: m.status as DataRepairReadinessStatus, summary: m.summary, action: m.action, objects};
}

function merge(report: DataRepairCaptureReadinessReport, frame: unknown): boolean {
  if (!frame || typeof frame !== 'object' || Buffer.byteLength(JSON.stringify(frame)) > probe.max_response_bytes) throw new Error('invalid capture probe frame');
  const f = frame as {findings?: {id: string; code: string; objects: string[]}[]; inspect_enrollment?: boolean};
  if (!Array.isArray(f.findings) || typeof f.inspect_enrollment !== 'boolean') throw new Error('invalid capture probe frame');
  const seen = new Set<string>();
  for (const row of f.findings) {
    if (seen.has(row.id) || !Array.isArray(row.objects)) throw new Error('invalid capture probe frame');
    seen.add(row.id);
    const index = report.findings.findIndex(old => old.id === row.id), old = report.findings[index];
    const objects = row.id === 'permissions' && row.code === 'writer_permissions_missing' && old?.code === row.code ? [...old.objects, ...row.objects] : row.objects;
    report.findings[index] = finding(row.id, row.code, objects);
  }
  return f.inspect_enrollment;
}

function aggregate(report: DataRepairCaptureReadinessReport): DataRepairCaptureReadinessReport {
  report.status = report.findings.some(f => f.status === 'blocked') ? 'blocked' : report.findings.some(f => f.status === 'unknown') ? 'unknown' : 'ready';
  return report;
}

/** Inspect one exclusively leased writer session. The pool must return idle
 * connections. This owns a read-only transaction and always rolls it back;
 * no installed capture/check function, business row or journal is executed/read.
 * Timeout destroys the leased connection instead of returning a busy client. */
export async function checkDataRepairCapture(pool: OperationPool, input: DataRepairCaptureReadinessOptions): Promise<DataRepairCaptureReadinessReport> {
  const config = options(input);
  const baseline = (): DataRepairCaptureReadinessReport => ({version: probe.version, profile: 'capture', ...config, checked_at: new Date().toISOString(), status: 'unknown',
    findings: probe.finding_ids.map(id => finding(id, id === 'connection' ? 'writer_unavailable' : 'not_inspected'))});
  const report = baseline();
  let connection: OperationConnection | undefined, stopped = false, released = false;
  const release = (discard: boolean) => {
    if (connection && !released) {
      released = true;
      try { connection.release(discard); } catch { report.findings[0] = finding('connection', 'writer_unavailable'); }
    }
  };
  const query = async (sql: string, values?: unknown[]) => {
    if (stopped || !connection || released) throw new Error('capture inspection stopped');
    const result = await connection.query(sql, values);
    if (stopped) throw new Error('capture inspection stopped');
    return result;
  };
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<DataRepairCaptureReadinessReport>(resolve => { timer = setTimeout(() => { stopped = true; release(true); resolve(baseline()); }, probe.timeout_ms); });
  const work = async () => {
    try {
      connection = await pool.connect();
      if (stopped) { release(true); return baseline(); }
      await query('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY');
      await query(probe.setup_sql);
      const catalog = await query(probe.catalog_sql, [config.receipt_protocol]);
      if (merge(report, catalog.rows[0]?.probe)) {
        const enrolled = await query(probe.enrollment_sql, [config.table]); merge(report, enrolled.rows[0]?.probe);
      }
    } catch {
      report.findings[0] = finding('connection', 'writer_unavailable');
    } finally {
      if (!released && connection) {
        try { await query('ROLLBACK'); release(false); } catch { release(true); report.findings[0] = finding('connection', 'writer_unavailable'); }
      }
    }
    return aggregate(report);
  };
  try { return await Promise.race([work(), timeout]); } finally { if (timer) clearTimeout(timer); }
}
