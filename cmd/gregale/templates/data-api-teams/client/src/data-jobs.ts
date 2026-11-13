import { dataRequest } from '@gregale/data'
import type { teamsClient } from './orders.js'
import { parseOrder } from './orders.js'
import type { Database } from './database.types.js'
export type DataJob = Database['api']['Tables']['team_order_jobs']['Row']
export type DataJobSummary = Pick<DataJob,'id'|'tenant_id'|'actor'|'kind'|'state'|'processed'|'total'|'created_at'|'attempts'|'next_attempt_at'|'last_error_code'>
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
function parse(value: unknown, tenant: string): DataJob {
  const job = value as DataJob
  if (!job || !uuid.test(job.id) || job.tenant_id !== tenant || typeof job.actor !== 'string' || !['import','export'].includes(job.kind) || !['queued','running','completed','cancelled','failed','blocked'].includes(job.state) || !Number.isSafeInteger(job.processed) || !Number.isSafeInteger(job.total) || job.processed < 0 || job.total < job.processed) throw new Error('Invalid background job')
  return job
}
export function backgroundDataJobs(client: ReturnType<typeof teamsClient>) {
  const one = async (query: PromiseLike<{ data: unknown; error: unknown }>, id: string) => {
    const response = await query
    if (response.error || !Array.isArray(response.data) || response.data.length !== 1) throw new Error('Job could not be confirmed; resolve the original operation')
    const job = parse(response.data[0], client.tenant)
    if (job.id !== id) throw new Error('Job response does not match the original operation')
    return job
  }
  return {
    /** Reuse operationId to resolve an uncertain enqueue; never generate a replacement ID. */
    queue(kind: 'import' | 'export', operationId: string, importId?: string) {
      if (!['import','export'].includes(kind) || !uuid.test(operationId) || (kind === 'import' && !uuid.test(importId ?? '')) || (kind === 'export' && importId !== undefined)) throw new Error('Invalid job operation')
      return one(dataRequest(client.db.rpc('start_team_order_job', { operation_id: operationId, team_id: client.tenant, job_kind: kind, source_import: importId ?? null }), { timeoutMs: 5000 }),operationId)
    },
    cancel(id: string) {
      if (!uuid.test(id)) throw new Error('Invalid job ID')
      return one(dataRequest(client.db.rpc('cancel_team_order_job', { job_id: id }), { timeoutMs: 5000 }),id)
    },
    resume(id: string) {
      if (!uuid.test(id)) throw new Error('Invalid job ID')
      return one(dataRequest(client.db.rpc('resume_team_order_job', { job_id: id }), { timeoutMs: 5000 }),id)
    },
    async list() {
      const response = await dataRequest(client.db.from('team_order_jobs').select('id,tenant_id,actor,kind,state,processed,total,created_at,attempts,next_attempt_at,last_error_code').eq('tenant_id', client.tenant).order('created_at', { ascending: false }).order('id').limit(20), { timeoutMs: 5000 })
      if (response.error || !Array.isArray(response.data)) throw new Error('Cannot load background jobs')
      return response.data.map(job => parse(job, client.tenant)) as DataJobSummary[]
    },
    get(id: string) {
      if (!uuid.test(id)) throw new Error('Invalid job ID')
      return one(dataRequest(client.db.from('team_order_jobs').select().eq('tenant_id', client.tenant).eq('id', id), { timeoutMs: 10000 }),id)
    },
  }
}
export function snapshotCsv(job: DataJob): string {
  const result = job.result as { rows?: unknown } | null
  if (job.kind !== 'export' || job.state !== 'completed' || !Array.isArray(result?.rows) || result.rows.length > 10000) throw new Error('Export snapshot unavailable')
  const columns = ['id','tenant_id','created_by','total_cents','status','version'] as const
  const cell = (value: unknown) => { const text = String(value); return '"' + (/^[\s\uFEFF]*[=+\-@]/.test(text) ? "'" + text : text).replaceAll('"','""') + '"' }
  return '\uFEFF' + columns.map(cell).join(',') + '\r\n' + result.rows.map(value => { const row = parseOrder(value); if (row.tenant_id !== job.tenant_id) throw new Error('Export scope changed'); return columns.map(column => cell(row[column])).join(',') + '\r\n' }).join('')
}
