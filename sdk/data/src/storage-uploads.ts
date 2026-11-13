import type { StorageObject, StorageUploadBody } from './storage.js'

export type UploadSession = Readonly<{
  id: string; bucket: string; name: string; size: number; content_type: string
  state: 'uploading' | 'finishing' | 'complete' | 'aborted'; expires_at: string; chunk_size: number
  parts: ReadonlyArray<{ number: number; size: number; sha256: string }>
  object?: StorageObject
}>
export type ResumableUploadOptions = {
  contentType?: string; signal?: AbortSignal; sessionId?: string
  /** Reuse this ID to reconcile an uncertain start through idempotent creation. */
  id?: string
  /** Persist the recovery ID before any create request is sent. */
  onSessionId?: (id: string) => void | Promise<void>
  /** Undefined replaces unconditionally, null creates only, otherwise compare the previous version. */
  expectedVersion?: string | null
  /** Persist this ID to resume without re-uploading acknowledged chunks. */
  onSession?: (session: UploadSession) => void | Promise<void>
  onProgress?: (progress: { sessionId: string; uploaded: number; total: number }) => void
}
export class UploadSessionStartError extends Error {
  constructor(readonly sessionId: string, cause: unknown) {
    super('Upload session start could not be confirmed; reconcile this session ID before retrying', { cause })
    this.name = 'UploadSessionStartError'
  }
}
export type StorageUploads = {
  startUpload(name: string, options: { size: number; contentType?: string; expectedVersion?: string | null; id?: string; signal?: AbortSignal }): Promise<UploadSession>
  getUpload(id: string, options?: { signal?: AbortSignal }): Promise<UploadSession>
  abortUpload(id: string, options?: { signal?: AbortSignal }): Promise<UploadSession>
  completeUpload(id: string, options?: { signal?: AbortSignal }): Promise<UploadSession>
  uploadPart(id: string, number: number, body: Blob, options?: { signal?: AbortSignal }): Promise<UploadSession>
  /** Stops on errors; call again with the saved sessionId to reconcile and resume. */
  uploadResumable(name: string, body: StorageUploadBody, options?: ResumableUploadOptions): Promise<StorageObject>
}
const idPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
function parseSession(value: unknown, bucket: string): UploadSession {
  const session = value as UploadSession
  if (!session || !idPattern.test(session.id) || session.bucket !== bucket || typeof session.name !== 'string' || !session.name || !Number.isSafeInteger(session.size) || session.size < 0 || session.size > 104857600 || session.chunk_size !== 5242880 || typeof session.content_type !== 'string' || !['uploading','finishing','complete','aborted'].includes(session.state) || !Number.isFinite(Date.parse(session.expires_at)) || !Array.isArray(session.parts) || session.parts.length > 20 || new Set(session.parts.map(p => p.number)).size !== session.parts.length || session.parts.some(p => !p || !Number.isInteger(p.number) || p.number < 1 || p.number > Math.ceil(session.size/session.chunk_size) || p.size !== Math.min(session.chunk_size,session.size-(p.number-1)*session.chunk_size) || !/^[a-f0-9]{64}$/.test(p.sha256))) throw new Error('Invalid upload session response')
  if (session.state === 'complete' && (!session.object || session.object.bucket !== bucket || session.object.name !== session.name || session.object.size !== session.size)) throw new Error('Invalid upload receipt')
  return session
}
function blobOf(value: StorageUploadBody, type: string): Blob {
  if (value instanceof Blob) return value
  if (ArrayBuffer.isView(value)) return new Blob([new Uint8Array(value.buffer, value.byteOffset, value.byteLength).slice().buffer], { type })
  return new Blob([value], { type })
}
async function digest(body: Blob): Promise<string> {
  return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', await body.arrayBuffer())), byte => byte.toString(16).padStart(2, '0')).join('')
}
export function storageUploads(bucket: string, call: (path: string, init: RequestInit) => Promise<Response>, validateName: (name: string) => unknown): StorageUploads {
  const path = (id?: string) => {
    if (!/^[a-z][a-z0-9_]{0,62}$/.test(bucket)) throw new Error('Invalid bucket name')
    if (id !== undefined && !idPattern.test(id)) throw new Error('Invalid upload session ID')
    return `upload/${bucket}${id ? `/${id}` : ''}`
  }
  const json = async (target: string, init: RequestInit): Promise<UploadSession> => parseSession(await (await call(target, init)).json(), bucket)
  const methods: StorageUploads = {
    async startUpload(name, { size, contentType = 'application/octet-stream', expectedVersion, id = crypto.randomUUID(), signal }) {
      validateName(name)
      if (!idPattern.test(id) || !Number.isSafeInteger(size) || size < 0 || size > 104857600 || (expectedVersion !== undefined && expectedVersion !== null && (typeof expectedVersion !== 'string' || !/^[0-9a-f]{32}$/.test(expectedVersion)))) throw new Error('Invalid upload session')
      return json(path(), { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ id, name, size, content_type: contentType, ...(expectedVersion !== undefined ? { expected_version: expectedVersion } : {}) }), signal })
    },
    getUpload: async (id, { signal } = {}) => json(path(id), { method: 'GET', signal }),
    abortUpload: async (id, { signal } = {}) => json(path(id), { method: 'DELETE', signal }),
    completeUpload: async (id, { signal } = {}) => json(`${path(id)}/complete`, { method: 'POST', signal }),
    async uploadPart(id, number, body, { signal } = {}) {
      if (!Number.isInteger(number) || number < 1 || number > 20 || body.size > 5242880) throw new Error('Invalid upload part')
      return json(`${path(id)}/parts/${number}`, { method: 'PUT', headers: { 'content-type': 'application/octet-stream', 'x-content-sha256': await digest(body) }, body, signal })
    },
    async uploadResumable(name, value, { contentType, signal, sessionId, id, expectedVersion, onSessionId, onSession, onProgress } = {}) {
      validateName(name)
      if (sessionId !== undefined && id !== undefined) throw new Error('Use either sessionId to resume or id for idempotent creation')
      const type = contentType ?? (value instanceof Blob && value.type ? value.type : 'application/octet-stream')
      const body = blobOf(value, type)
      let session: UploadSession
      if (sessionId !== undefined) session = await methods.getUpload(sessionId, { signal })
      else {
        const recoveryId = id ?? crypto.randomUUID()
        if (!idPattern.test(recoveryId)) throw new Error('Invalid upload session ID')
        signal?.throwIfAborted()
        await onSessionId?.(recoveryId)
        try { session = await methods.startUpload(name, { size: body.size, contentType: type, expectedVersion, id: recoveryId, signal }) }
        catch (cause) { throw new UploadSessionStartError(recoveryId, cause) }
      }
      if (session.name !== name || session.size !== body.size || session.content_type !== type) throw new Error('Upload session does not match this file')
      await onSession?.(session)
      if (session.state === 'aborted') throw new Error('Upload session was aborted')
      {
        let uploaded = 0
        for (let offset = 0, number = 1; offset < body.size; offset += session.chunk_size, number++) {
          signal?.throwIfAborted()
          const part = body.slice(offset, offset + session.chunk_size)
          const known = session.parts.find(p => p.number === number)
          if (known && (known.size !== part.size || known.sha256 !== await digest(part))) throw new Error('Upload session contains different file bytes')
          if (!known) {
            if (session.state !== 'uploading') throw new Error('Upload receipt is missing a file checksum')
            session = await methods.uploadPart(session.id, number, part, { signal })
          }
          uploaded += part.size
          onProgress?.({ sessionId: session.id, uploaded, total: body.size })
        }
      }
      if (session.state !== 'complete') session = await methods.completeUpload(session.id, { signal })
      if (!session.object) throw new Error('Upload receipt unavailable; reconcile the session before retrying')
      return session.object
    },
  }
  return methods
}
