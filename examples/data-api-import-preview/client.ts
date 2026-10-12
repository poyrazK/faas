import { createDataClient, dataRequest } from '@gregale/data'
import type { Database } from './database.types.js'
export type ImportRow = { key: string; body: string }
export type ImportApproval = { identity: {subject:string;tenant:string}; items: ImportRow[]; expected: {id: string | null; version: number | null}[] }
export type ImportPreview = { identity: {subject:string;tenant:string}; items: unknown[]; total: number; valid: number; rows: { index: number; action: 'insert' | 'update' | 'unchanged' | 'invalid'; errors: string[]; expected_id: string | null; expected_version: number | null }[] }

/** Validate the JSON RPC response before exposing it to a review screen. */
export function parsePreview(value: unknown): ImportPreview {
  if (!value || typeof value !== 'object') throw new Error('Invalid import preview')
  const preview = value as ImportPreview
  if (!Number.isSafeInteger(preview.total) || preview.total < 0 || !Number.isSafeInteger(preview.valid) || preview.valid < 0 || preview.valid > preview.total || !Array.isArray(preview.items) || preview.items.length!==preview.total || !Array.isArray(preview.rows) || preview.rows.length !== preview.total) throw new Error('Invalid import preview')
  if (!preview.identity || typeof preview.identity.subject!=='string' || !preview.identity.subject || typeof preview.identity.tenant!=='string' || !preview.identity.tenant) throw new Error('Invalid import preview')
  for (const [index,row] of preview.rows.entries()) {
    if (!row || row.index !== index || !['insert','update','unchanged','invalid'].includes(row.action) || !Array.isArray(row.errors) || !row.errors.every(error=>typeof error==='string') || (row.action==='invalid') !== (row.errors.length>0)) throw new Error('Invalid import preview')
    if (row.expected_id===null ? row.expected_version!==null : typeof row.expected_id!=='string' || !/^[1-9][0-9]{0,18}$/.test(row.expected_id) || !Number.isSafeInteger(row.expected_version) || (row.expected_version ?? 0)<=0) throw new Error('Invalid import preview')
    if ((row.action==='insert' || row.action==='invalid') !== (row.expected_id===null)) throw new Error('Invalid import preview')

  }
  if (preview.rows.filter(row=>row.action!=='invalid').length!==preview.valid) throw new Error('Invalid import preview')
  return preview
}
export async function previewImport(url: string, token: string, items: readonly ImportRow[]) {
  const db = createDataClient<Database>({ url, accessToken: token }).schema('api')
  const response = await dataRequest(db.rpc('preview_import', { items: [...items] }), { timeoutMs: 5000 })
  if (response.error) throw response.error
  return parsePreview(response.data)
}
/** Commit only after review; server validation runs again in this transaction. */
export async function commitImport(url: string, token: string, approval: ImportApproval) {
  const db = createDataClient<Database>({ url, accessToken: token }).schema('api')
  return dataRequest(db.rpc('commit_import', { items: approval.items, expected: approval.expected, review_identity: approval.identity }), { timeoutMs: 5000 })
}

/** Snapshot the reviewed payload and versions; edits require a fresh preview. */
export function approveImport(preview: ImportPreview): ImportApproval {
  parsePreview(preview)
  if (preview.valid!==preview.total) throw new Error('Resolve import validation errors before approval')
  const items = preview.items.map(value => {
    if (!value || typeof value!=='object') throw new Error('Invalid reviewed input')
    const row=value as ImportRow
    if (typeof row.key!=='string' || typeof row.body!=='string') throw new Error('Invalid reviewed input')
    return {key:row.key,body:row.body}
  })
  return {identity:{...preview.identity},items,expected:preview.rows.map(row=>({id:row.expected_id,version:row.expected_version}))}
}
