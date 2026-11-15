import { createHash, randomUUID } from 'node:crypto';
import { open, rename, readFile, unlink } from 'node:fs/promises';
import { basename, dirname, join, resolve } from 'node:path';
import type { ClientConfig } from './client-instance.js';
import { scopedRequest } from './request-scope.js';
import type { ApiRequestOptions } from './generated/core/ApiRequestOptions.js';
import type { RequestOptions } from './request-options.js';
import type { UploadDeployOptions } from './generated/models/UploadDeployOptions.js';
import type { DeploymentSubmissionReceipt } from './generated/models/DeploymentSubmissionReceipt.js';

const GRAIN = 1024 * 1024;
export type SourceUploadStage = 'new' | 'uploading' | 'ready' | 'committing' | 'accepted';
export type SourceUploadCode = 'invalid_checkpoint' | 'source_changed' | 'invalid_response' | 'session_expired' | 'session_closed'
  | 'recovery_limit' | 'request_failed' | 'checkpoint_failed' | 'acceptance_unknown' | 'not_ready' | 'timeout';
/** Portable JSON; contains no source path, deployment metadata values or credentials. */
export interface SourceUploadCheckpoint {
  readonly version: 1; readonly base_url: string; readonly app_slug: string; readonly total_size: number;
  readonly sha256_hex: string; readonly options_sha256: string; readonly start_key: string; readonly attempt_id: string; readonly commit_key: string;
  readonly stage: SourceUploadStage; readonly upload_id: string | null; readonly chunk_size: number; readonly received_bytes: number;
  readonly pending_end: number | null; readonly deployment_id: string | null; readonly expires_at: string | null;
}
export interface SourceUploadProgress { readonly stage: SourceUploadStage; readonly received_bytes: number; readonly total_size: number; }
export interface SourceUploadOptions extends Omit<RequestOptions, 'timeoutMs'> {
  readonly timeoutMs: number;
  /** Awaited before writes. Save durably; serialize one writer per checkpoint. */
  readonly onCheckpoint: (checkpoint: SourceUploadCheckpoint) => void | Promise<void>;
  readonly maxRecoveries?: number;
  readonly onProgress?: (progress: SourceUploadProgress) => void | Promise<void>;
}
export interface SourceUploadInput {
  readonly path: string; readonly appSlug: string; readonly checkpoint?: SourceUploadCheckpoint; readonly deployOptions?: UploadDeployOptions;
}
export interface SourceUploads {
  uploadSource(input: SourceUploadInput, options: SourceUploadOptions): Promise<SourceUploadCheckpoint>;
  commitSource(input: { readonly checkpoint: SourceUploadCheckpoint }, options: SourceUploadOptions): Promise<DeploymentSubmissionReceipt>;
}
export class SourceUploadError extends Error {
  override readonly name: string;
  constructor(readonly code: SourceUploadCode, readonly checkpoint?: SourceUploadCheckpoint, override readonly cause?: unknown) {
    super(`SDK source upload stopped: ${code}`); this.name = code === 'timeout' ? 'TimeoutError' : 'SourceUploadError';
  }
}
const integer = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const id = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,254}$/.test(value);
const hash = (value: string | Uint8Array) => createHash('sha256').update(value).digest('hex');
function base(value: string): string {
  const url = new URL(value);
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw new TypeError('Invalid API base URL');
  return url.href.replace(/\/+$/, '');
}
function canonical(value: unknown): unknown {
  if (value === null) return ['null'];
  if (typeof value === 'boolean') return ['bool', value];
  if (typeof value === 'string') {
    if (Buffer.from(value).toString('utf8') !== value) throw new TypeError('Deployment metadata requires valid Unicode');
    return ['str', value];
  }
  if (typeof value === 'number') {
    if (!Number.isFinite(value) || Math.abs(value) > Number.MAX_SAFE_INTEGER) throw new TypeError('Deployment metadata requires finite safe numbers');
    const bytes = Buffer.alloc(8); bytes.writeDoubleBE(value || 0); return ['num', bytes.toString('hex')];
  }
  if (Array.isArray(value)) return ['list', value.map(canonical)];
  if (value && typeof value === 'object' && Object.getPrototypeOf(value) === Object.prototype) {
    return ['dict', Object.keys(value).filter(key => (value as Record<string, unknown>)[key] !== undefined).sort((a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b))).map(key => { canonical(key); return [key, canonical((value as Record<string, unknown>)[key])]; })];
  }
  throw new TypeError('Deployment metadata must be JSON data');
}
/** Internal shared fingerprint for composition; presence is significant. */
export function sourceUploadMetadataHash(value: UploadDeployOptions | undefined): string {
  return hash(JSON.stringify(canonical({ present: value !== undefined, value: value ?? {} })));
}
export function parseSourceUploadCheckpoint(value: unknown): SourceUploadCheckpoint {
  const bad = () => { throw new TypeError('Invalid source upload checkpoint'); };
  if (!value || typeof value !== 'object') return bad();
  const v = value as SourceUploadCheckpoint;
  const keys = ['version','base_url','app_slug','total_size','sha256_hex','options_sha256','start_key','attempt_id','commit_key','stage','upload_id','chunk_size','received_bytes','pending_end','deployment_id','expires_at'];
  if (Object.keys(v).length !== keys.length || keys.some(key => !Object.hasOwn(v, key)) || v.version !== 1 || typeof v.base_url !== 'string' || base(v.base_url) !== v.base_url ||
      typeof v.app_slug !== 'string' || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(v.app_slug) || !integer(v.total_size) || !v.total_size ||
      typeof v.sha256_hex !== 'string' || !/^[a-f0-9]{64}$/.test(v.sha256_hex) || typeof v.options_sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(v.options_sha256) ||
      ![v.start_key,v.attempt_id,v.commit_key].every(id) || !['new','uploading','ready','committing','accepted'].includes(v.stage) ||
      !integer(v.received_bytes) || v.received_bytes > v.total_size || !integer(v.chunk_size)) return bad();
  if (v.stage === 'new' ? v.upload_id !== null || v.chunk_size !== 0 || v.received_bytes !== 0 || v.pending_end !== null || v.expires_at !== null
    : !id(v.upload_id) || !v.chunk_size || typeof v.expires_at !== 'string' || !v.expires_at) return bad();
  if (v.pending_end !== null && (v.stage !== 'uploading' || !integer(v.pending_end) || v.pending_end <= v.received_bytes || v.pending_end > v.total_size)) return bad();
  if (['ready','committing','accepted'].includes(v.stage) && (v.received_bytes !== v.total_size || v.pending_end !== null)) return bad();
  if (v.stage === 'accepted' ? !id(v.deployment_id) : v.deployment_id !== null) return bad();
  return Object.freeze({ ...v });
}
async function writeCheckpoint(path: string, checkpoint: SourceUploadCheckpoint): Promise<void> {
  const value = parseSourceUploadCheckpoint(checkpoint);
  const temporary = join(dirname(path), `.${basename(path)}.${randomUUID()}`);
  const file = await open(temporary, 'wx', 0o600);
  try {
    try { await file.writeFile(JSON.stringify(value)); await file.sync(); } finally { await file.close(); }
    await rename(temporary, path);
    if (process.platform !== 'win32') {
      const directory = await open(dirname(path), 'r'); try { await directory.sync(); } finally { await directory.close(); }
    }
  } finally { await unlink(temporary).catch(error => { if (error.code !== 'ENOENT') throw error; }); }
}
const checkpointWrites = new Map<string, Promise<void>>();
/** Serialize local saves so a timed-out callback cannot overwrite a newer state. */
export function saveSourceUploadCheckpoint(path: string, checkpoint: SourceUploadCheckpoint): Promise<void> {
  const key = resolve(path), value = parseSourceUploadCheckpoint(checkpoint);
  const pending = (checkpointWrites.get(key) ?? Promise.resolve()).catch(() => {}).then(() => writeCheckpoint(key, value));
  const tracked = pending.finally(() => { if (checkpointWrites.get(key) === tracked) checkpointWrites.delete(key); });
  checkpointWrites.set(key, tracked);
  return tracked;
}
export async function loadSourceUploadCheckpoint(path: string): Promise<SourceUploadCheckpoint> {
  const key = resolve(path); await checkpointWrites.get(key);
  return parseSourceUploadCheckpoint(JSON.parse(await readFile(key, 'utf8')));
}
function status(error: unknown): number | undefined {
  if (error && typeof error === 'object' && 'status' in error && typeof error.status === 'number') return error.status;
  return undefined;
}
async function readAt(path: string, offset: number, length: number, total: number): Promise<Buffer> {
  const file = await open(path, 'r');
  try {
    const info = await file.stat();
    if (!info.isFile() || info.size !== total) throw new Error('Source changed');
    const bytes = Buffer.alloc(length); let read = 0;
    while (read < length) {
      const result = await file.read(bytes, read, length - read, offset + read);
      if (!result.bytesRead) throw new Error('Source changed'); read += result.bytesRead;
    }
    return bytes;
  } finally { await file.close(); }
}
class Operation {
  readonly config: ClientConfig; readonly options: SourceUploadOptions; readonly controller = new AbortController();
  checkpoint?: SourceUploadCheckpoint; recoveries = 0; deadline = Infinity;
  constructor(config: ClientConfig, options: SourceUploadOptions, checkpoint?: SourceUploadCheckpoint) {
    if (!Number.isInteger(options?.timeoutMs) || options.timeoutMs <= 0 || options.timeoutMs > 2 ** 31 - 1 ||
        !integer(options.maxRecoveries ?? 3) || (options.maxRecoveries ?? 3) > 100 || typeof options.onCheckpoint !== 'function' ||
        options.onProgress !== undefined && typeof options.onProgress !== 'function') throw new TypeError('Invalid source upload options');
    this.config = { ...config, requestFetch: policy => {
      const send = config.requestFetch(policy);
      return (input, init) => send(input, { ...init, redirect: 'error' });
    } }; this.options = { ...options, retry: options.retry && { ...options.retry } };
    this.checkpoint = checkpoint === undefined ? undefined : parseSourceUploadCheckpoint(checkpoint);
    if (this.checkpoint && this.checkpoint.base_url !== base(config.BASE)) throw this.error('invalid_checkpoint');
  }
  error(code: SourceUploadCode, cause?: unknown) { return new SourceUploadError(code, this.checkpoint, cause); }
  check() { if (performance.now() >= this.deadline && !this.controller.signal.aborted) this.controller.abort(this.error('timeout')); this.controller.signal.throwIfAborted(); }
  async emit(value: SourceUploadCheckpoint) {
    this.check(); this.checkpoint = parseSourceUploadCheckpoint(value);
    try { await this.options.onCheckpoint(this.checkpoint); } catch (error) { this.check(); throw this.error('checkpoint_failed', error); }
    this.check();
    await this.options.onProgress?.(Object.freeze({ stage: value.stage, received_bytes: value.received_bytes, total_size: value.total_size })); this.check();
  }
  async request<T>(api: ApiRequestOptions): Promise<T> {
    this.check();
    return scopedRequest<T>(this.config, api, { signal: this.controller.signal, timeoutMs: 0,
      retry: api.method === 'GET' ? this.options.retry : false, onResponse: this.options.onResponse });
  }
  recover(error: unknown) {
    this.check(); const code = status(error);
    if (code === 410) throw this.error('session_expired', error);
    if (code !== undefined && code !== 409 && code < 500) throw this.error('request_failed', error);
    if (++this.recoveries > (this.options.maxRecoveries ?? 3)) throw this.error('recovery_limit', error);
  }
  async session(): Promise<SourceUploadCheckpoint> {
    const c = this.checkpoint!;
    let value: Record<string, unknown>;
    try { value = await this.request({ method: 'GET', url: '/v1/uploads/{id}', path: { id: c.upload_id } }); }
    catch (error) { if (status(error) === 410) throw this.error('session_expired', error); throw error; }
    if (!value || value.upload_id !== c.upload_id || value.app_slug !== c.app_slug || value.total_size !== c.total_size ||
      value.chunk_size !== c.chunk_size || !integer(value.received_bytes) || value.received_bytes > c.total_size || value.received_bytes < c.received_bytes || typeof value.expires_at !== 'string') throw this.error('invalid_response');
    if (value.status === 'expired') throw this.error('session_expired');
    if (value.status === 'cancelled') throw this.error('session_closed');
    if (value.status === 'committed') {
      if (!id(value.deployment_id) || value.received_bytes !== c.total_size) throw this.error('invalid_response');
      return parseSourceUploadCheckpoint({ ...c, stage: 'accepted', received_bytes: c.total_size, pending_end: null, deployment_id: value.deployment_id });
    }
    if (value.status !== 'open' || value.received_bytes > (c.pending_end ?? c.received_bytes)) throw this.error('invalid_response');
    return parseSourceUploadCheckpoint({ ...c, received_bytes: value.received_bytes, pending_end: null });
  }
  async run<T>(work: () => Promise<T>): Promise<T> {
    let rejectAbort!: (reason: unknown) => void;
    const stopped = new Promise<never>((_resolve,reject) => { rejectAbort = reject; });
    const abort = () => rejectAbort(this.controller.signal.reason);
    const callerAbort = () => this.controller.abort(this.options.signal?.reason);
    this.controller.signal.addEventListener('abort', abort, { once: true });
    this.options.signal?.addEventListener('abort', callerAbort, { once: true });
    this.deadline = performance.now() + this.options.timeoutMs;
    const timer = setTimeout(() => this.controller.abort(this.error('timeout')), this.options.timeoutMs);
    try {
      if (this.options.signal?.aborted) callerAbort();
      return await Promise.race([stopped, work()]);
    } catch (error) {
      if (this.controller.signal.aborted) throw this.controller.signal.reason;
      if (error instanceof SourceUploadError) throw error;
      throw this.error('request_failed', error);
    } finally {
      clearTimeout(timer); this.options.signal?.removeEventListener('abort', callerAbort);
      this.controller.signal.removeEventListener('abort', abort);
      this.controller.abort(); // Late callbacks/filesystem work cannot send further requests.
    }
  }
}
export function bindSourceUploads(getConfig: () => ClientConfig): SourceUploads {
  return {
    async uploadSource(input, options) {
      const op = new Operation(getConfig(), options, input.checkpoint);
      const metadata = structuredClone(input.deployOptions ?? {});
      const metadataHash = sourceUploadMetadataHash(input.deployOptions === undefined ? undefined : metadata);
      const path = resolve(input.path), appSlug = input.appSlug, hasMetadata = input.deployOptions !== undefined;
      return op.run(async () => {
        op.check();
        let size: number, digest: string; const grains: string[] = [];
        try {
          const file = await open(path, 'r');
          try { const info = await file.stat(); if (!info.isFile() || !integer(info.size) || !info.size) throw new Error('Source must be a regular archive'); size = info.size; }
          finally { await file.close(); }
          const fingerprint = createHash('sha256');
          for (let offset = 0; offset < size; offset += GRAIN) { op.check(); const data = await readAt(path, offset, Math.min(GRAIN,size-offset),size); fingerprint.update(data); grains.push(hash(data)); }
          digest = fingerprint.digest('hex'); op.check();
        } catch (error) { op.check(); throw op.error('source_changed', error); }
        let current = op.checkpoint;
        if (!current) {
          current = parseSourceUploadCheckpoint({ version: 1, base_url: base(op.config.BASE), app_slug: appSlug, total_size: size, sha256_hex: digest,
            options_sha256: metadataHash, start_key: randomUUID(), attempt_id: randomUUID(), commit_key: randomUUID(), stage: 'new', upload_id: null,
            chunk_size: 0, received_bytes: 0, pending_end: null, deployment_id: null, expires_at: null });
          await op.emit(current);
        } else if (current.app_slug !== appSlug || current.options_sha256 !== metadataHash) throw op.error('invalid_checkpoint');
        else if (current.total_size !== size || current.sha256_hex !== digest) throw op.error('source_changed');
        if (current.stage === 'new') {
          let value: Record<string, unknown>;
          while (true) {
            try { value = await op.request({ method: 'POST', url: '/v1/uploads', headers: { 'Idempotency-Key': current.start_key },
              body: { app_slug: appSlug, total_size: size, sha256_hex: digest, ...(!hasMetadata ? {} : { deploy_options: metadata }) }, mediaType: 'application/json' }); break; }
            catch (error) { op.recover(error); }
          }
          if (!value || !id(value.upload_id) || value.total_size !== size || !integer(value.chunk_size) || !value.chunk_size || typeof value.expires_at !== 'string' || !value.expires_at) throw op.error('invalid_response');
          current = parseSourceUploadCheckpoint({ ...current, stage: 'uploading', upload_id: value.upload_id, chunk_size: value.chunk_size, expires_at: value.expires_at }); await op.emit(current);
        }
        current = await op.session(); await op.emit(current);
        if (current.stage === 'committing' || current.stage === 'accepted') return current;
        while (current.received_bytes < size) {
          const offset = current.received_bytes, end = Math.min(size, offset + current.chunk_size, offset + 16 * GRAIN);
          const parts: Buffer[] = []; let position = offset;
          while (position < end) {
            const grainOffset = Math.floor(position / GRAIN) * GRAIN;
            let grain: Buffer;
            try { grain = await readAt(path, grainOffset, Math.min(GRAIN, size - grainOffset), size); }
            catch (error) { op.check(); throw op.error('source_changed', error); }
            op.check(); if (hash(grain) !== grains[Math.floor(position / GRAIN)]) throw op.error('source_changed');
            const piece = grain.subarray(position - grainOffset, Math.min(grain.length, end - grainOffset));
            parts.push(piece); position += piece.length;
          }
          const data = Buffer.concat(parts);
          current = parseSourceUploadCheckpoint({ ...current, stage: 'uploading', pending_end: offset + data.length }); await op.emit(current);
          let next: string;
          try { next = await op.request({ method: 'PATCH', url: '/v1/uploads/{id}', path: { id: current.upload_id },
            headers: { 'Upload-Offset': offset, 'Idempotency-Key': hash(`${current.upload_id}:${offset}:${hash(data)}`) },
            body: new Blob([new Uint8Array(data.buffer as ArrayBuffer, data.byteOffset, data.byteLength)]), mediaType: 'application/offset+octet-stream', responseHeader: 'Upload-Offset' }); }
          catch (error) { op.recover(error); current = await op.session(); await op.emit(current); if (current.stage === 'accepted') return current; continue; }
          if (typeof next !== 'string' || !/^(0|[1-9][0-9]*)$/.test(next) || !integer(Number(next)) || Number(next) !== offset+data.length) throw op.error('invalid_response');
          current = parseSourceUploadCheckpoint({ ...current, received_bytes: Number(next), pending_end: null }); await op.emit(current);
        }
        current = parseSourceUploadCheckpoint({ ...current, stage: 'ready' }); await op.emit(current); return current;
      });
    },
    async commitSource({ checkpoint }, options) {
      const op = new Operation(getConfig(),options,checkpoint);
      async function receipt(): Promise<DeploymentSubmissionReceipt | undefined> {
        const current = op.checkpoint!;
        let value: DeploymentSubmissionReceipt;
        try { value = await op.request({ method: 'GET', url: '/v1/apps/{slug}/deployment-submissions/{attempt_id}', path: { slug: current.app_slug, attempt_id: current.attempt_id } }); }
        catch (error) { if (status(error) === 404) return undefined; throw error; }
        if (!value || value.attempt_id !== current.attempt_id || !/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(value.deployment_id) || !/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(value.app_id) || typeof value.accepted_at !== 'string' || !Number.isFinite(Date.parse(value.accepted_at)) ||
          current.deployment_id !== null && value.deployment_id !== current.deployment_id) throw op.error('invalid_response');
        await op.emit(parseSourceUploadCheckpoint({ ...current, stage: 'accepted', received_bytes: current.total_size, pending_end: null, deployment_id: value.deployment_id }));
        return value;
      }
      return op.run(async () => {
        let current = op.checkpoint!;
        if (current.stage === 'new') throw op.error('not_ready');
        let found = await receipt(); if (found) return found;
        if (current.stage === 'committing' || current.stage === 'accepted') throw op.error('acceptance_unknown');
        current = await op.session(); await op.emit(current);
        if (current.stage === 'accepted') { found = await receipt(); if (!found) throw op.error('acceptance_unknown'); return found; }
        if (current.received_bytes !== current.total_size) throw op.error('not_ready');
        current = parseSourceUploadCheckpoint({ ...current, stage: 'committing' }); await op.emit(current);
        let commitError: unknown;
        try { await op.request({ method: 'POST', url: '/v1/uploads/{id}/commit', path: { id: current.upload_id },
          headers: { 'Deployment-Attempt-ID': current.attempt_id, 'Idempotency-Key': current.commit_key } }); }
        catch (error) { op.check(); commitError = error; }
        found = await receipt(); if (!found) throw op.error('acceptance_unknown', commitError); return found;
      });
    },
  };
}
