import { createHash } from 'node:crypto';
import { OpenAPI } from './generated/core/OpenAPI.js';
import { RunsService } from './generated/services/RunsService.js';
import type { ExecutionFileResponse } from './generated/models/ExecutionFileResponse.js';

type FileRequests = Pick<typeof RunsService, 'prepareExecutionFile' | 'uploadExecutionFile'>;

const maxFileBytes = 64 * 1024 * 1024;
export interface PreparedExecutionFile { readonly file: Blob; readonly metadata: ExecutionFileResponse }
/** Hash incrementally and prepare a stable locator. This does not reserve storage. */
export async function prepareExecutionFile(file: Blob, signal?: AbortSignal, requests: FileRequests = RunsService): Promise<PreparedExecutionFile> {
  if (!Number.isSafeInteger(file.size) || file.size < 0 || file.size > maxFileBytes) throw new Error('Run file exceeds the 64 MiB hard limit');
  const hash = createHash('sha256');
  const reader = file.stream().getReader();
  try {
    for (;;) {
      signal?.throwIfAborted();
      const { value, done } = await reader.read();
      if (done) break;
      hash.update(value);
    }
  } finally { await reader.cancel(); reader.releaseLock(); }
  const pending = requests.prepareExecutionFile({ requestBody: { size_bytes: file.size, sha256: `sha256:${hash.digest('hex')}` } });
  const abort = () => pending.cancel();
  signal?.throwIfAborted();
  signal?.addEventListener('abort', abort, { once: true });
  try { return { file, metadata: await pending }; }
  finally { signal?.removeEventListener('abort', abort); }
}
/** Publish immutable bytes with backpressure supplied by fetch's Blob transport. */
export async function uploadPreparedExecutionFile(prepared: PreparedExecutionFile, signal?: AbortSignal, requests: FileRequests = RunsService): Promise<ExecutionFileResponse> {
  signal?.throwIfAborted();
  const pending = requests.uploadExecutionFile({ id: prepared.metadata.id, xContentSha256: prepared.metadata.sha256 ?? '', requestBody: prepared.file });
  const abort = () => pending.cancel();
  signal?.addEventListener('abort', abort, { once: true });
  try { return await pending; } finally { signal?.removeEventListener('abort', abort); }
}
/** Stream and verify a retained file. Publish the destination only after success. */
export async function downloadExecutionFile(id: string, destination: WritableStream<Uint8Array>, signal?: AbortSignal): Promise<ExecutionFileResponse> {
  const meta = await RunsService.getExecutionFile({ id });
  if (meta.id !== id || !Number.isSafeInteger(meta.size_bytes) || meta.size_bytes < 0 || meta.size_bytes > maxFileBytes || !/^sha256:[0-9a-f]{64}$/.test(meta.sha256 ?? '')) throw new Error('Invalid Run file metadata');
  const path = `/v1/execution-files/${encodeURIComponent(id)}/content`;
  const options = { method: 'GET' as const, url: path };
  const extra = typeof OpenAPI.HEADERS === 'function' ? await OpenAPI.HEADERS(options) : OpenAPI.HEADERS;
  const headers = new Headers(extra);
  const token = typeof OpenAPI.TOKEN === 'function' ? await OpenAPI.TOKEN(options) : OpenAPI.TOKEN;
  if (token && !headers.has('Authorization')) headers.set('Authorization', `Bearer ${token}`);
  const response = await fetch(new URL(path, OpenAPI.BASE), { headers, signal, ...(OpenAPI.WITH_CREDENTIALS ? { credentials: OpenAPI.CREDENTIALS } : {}) });
  if (!response.ok || !response.body) throw new Error(`Run file download failed (${response.status})`);
  const reader = response.body.getReader(); const writer = destination.getWriter(); const hash = createHash('sha256'); let size = 0;
  try {
    for (;;) {
      const { value, done } = await reader.read(); if (done) break;
      size += value.byteLength; if (size > meta.size_bytes) throw new Error('Run file download exceeds declared size');
      hash.update(value); await writer.write(value);
    }
    if (size !== meta.size_bytes || `sha256:${hash.digest('hex')}` !== meta.sha256) throw new Error('Run file checksum or length mismatch');
    await writer.close(); return meta;
  } catch (error) { await writer.abort(error); throw error; }
  finally { await reader.cancel(); reader.releaseLock(); writer.releaseLock(); }
}
