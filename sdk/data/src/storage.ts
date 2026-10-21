import { authenticatedFetch, dataAPIBase, requestIdOf, type DataClientOptions } from './client-fetch.js'

/**
 * A stored file's row in storage_objects (ADR-20261021101510). The owner's
 * row-level security policies on that table decide who may upload, download
 * and remove each file.
 */
export type StorageObject = Readonly<{
  bucket: string
  name: string
  owner: string | null
  size: number
  content_type: string
  created_at: string
  updated_at: string
}>

/** A refused or failed storage request, with the Data API's stable code. */
export class StorageError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId: string | null
  constructor(status: number, code: string, requestId: string | null) {
    super(`storage request failed: ${code} (${status})`)
    this.name = 'StorageError'
    this.status = status
    this.code = code
    this.requestId = requestId
  }
}

export type StorageUploadBody = Blob | ArrayBuffer | ArrayBufferView | string

export type StorageBucket = {
  /** Stores a file, replacing any file of the same name the caller may update. */
  upload(name: string, body: StorageUploadBody, options?: { contentType?: string; signal?: AbortSignal }): Promise<StorageObject>
  /** Fetches a file; read it with blob(), arrayBuffer() or its body stream. */
  download(name: string, options?: { range?: { start: number; end?: number }; signal?: AbortSignal }): Promise<Response>
  /** A link that works for 60 seconds without a token, for img, video and download links. */
  signedUrl(name: string, options?: { signal?: AbortSignal }): Promise<{ url: string; expiresAt: string }>
  /** Removes a file. */
  remove(name: string, options?: { signal?: AbortSignal }): Promise<void>
}

export type StorageClient = { from(bucket: string): StorageBucket }

const bucketPattern = /^[a-z][a-z0-9_]{0,62}$/

function objectPath(bucket: string, name: string): string {
  if (!bucketPattern.test(bucket)) throw new Error('Invalid bucket name')
  const segments = name.split('/')
  if (!name || segments.some(s => s === '' || s === '.' || s === '..')) throw new Error('Invalid object name')
  return `${bucket}/${segments.map(encodeURIComponent).join('/')}`
}

async function failure(response: Response): Promise<StorageError> {
  let code = 'storage_request_failed'
  try {
    const body = await response.json() as { code?: unknown }
    if (typeof body.code === 'string') code = body.code
  } catch { /* The status still explains it. */ }
  return new StorageError(response.status, code, requestIdOf(response))
}

/**
 * Files stored through the Data API, governed by the same tokens and RLS as
 * its rows. Accepts the options of createDataClient.
 *
 *   const storage = createStorageClient({ url, accessToken: () => session.token })
 *   await storage.from('uploads').upload(`${userId}/avatar.png`, file)
 *   const { url } = await storage.from('uploads').signedUrl(`${userId}/avatar.png`)
 */
export function createStorageClient(options: DataClientOptions): StorageClient {
  const base = `${dataAPIBase(options.url)}/storage/v1`
  const send = authenticatedFetch(options)
  const call = async (path: string, init: RequestInit) => {
    const response = await send(`${base}/${path}`, init)
    if (!response.ok) throw await failure(response)
    return response
  }
  return {
    from(bucket) {
      return {
        async upload(name, body, { contentType, signal } = {}) {
          const type = contentType ?? (typeof Blob !== 'undefined' && body instanceof Blob && body.type ? body.type : 'application/octet-stream')
          const response = await call(`object/${objectPath(bucket, name)}`, { method: 'PUT', body: body as BodyInit, headers: { 'Content-Type': type }, signal })
          return await response.json() as StorageObject
        },
        async download(name, { range, signal } = {}) {
          const headers: Record<string, string> = {}
          if (range) {
            if (!Number.isSafeInteger(range.start) || range.start < 0 || (range.end !== undefined && (!Number.isSafeInteger(range.end) || range.end < range.start))) throw new Error('Invalid range')
            headers.Range = `bytes=${range.start}-${range.end ?? ''}`
          }
          return call(`object/${objectPath(bucket, name)}`, { method: 'GET', headers, signal })
        },
        async signedUrl(name, { signal } = {}) {
          const response = await call(`sign/${objectPath(bucket, name)}`, { method: 'POST', signal })
          const body = await response.json() as { url: string; expires_at: string }
          return { url: body.url, expiresAt: body.expires_at }
        },
        async remove(name, { signal } = {}) {
          await call(`object/${objectPath(bucket, name)}`, { method: 'DELETE', signal })
        },
      }
    },
  }
}
