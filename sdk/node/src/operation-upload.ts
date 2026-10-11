import { createHash } from 'node:crypto';
import { OperationHTTPError, type OperationArtifact } from './customer-operations.js';
import type { OperationArtifactUploadRequest } from './generated/models/OperationArtifactUploadRequest.js';
import { OPERATION_UPLOAD_DEFAULT_BYTES, OPERATION_REPORT_ID_BYTES, OPERATION_ARTIFACT_NAME_BYTES } from './operation-contract.js';

export interface OperationDirectUploadInput {
  report_id: string;
  name: string;
  data: string | Uint8Array;
  /** Optional application memory bound; the API independently checks quotas. */
  maxBytes?: number;
}

interface UploadTransport {
  guard(): void;
  checkpoint(): Promise<void>;
  aborted(): boolean;
  lookup(declaration: OperationArtifactUploadRequest): Promise<{available: boolean; artifact?: OperationArtifact}>;
  upload(declaration: OperationArtifactUploadRequest, bytes: Buffer): Promise<{available: boolean; artifact?: OperationArtifact}>;
}
class OperationUploadProtocolError extends Error {}

/** One request/task lifetime. Transport retries never rerun business code. */
export function operationUploader(id: string, transport: UploadTransport): (input: OperationDirectUploadInput) => Promise<OperationArtifact> {
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
  const uploads = new Map<string, { fingerprint: string; pending?: Promise<OperationArtifact>; artifact?: OperationArtifact }>();
  return async (input: OperationDirectUploadInput): Promise<OperationArtifact> => {
    transport.guard();
    const maxBytes = input.maxBytes ?? OPERATION_UPLOAD_DEFAULT_BYTES;
    const text = (value: string, limit: number): boolean => typeof value === 'string' && value.length > 0 && Buffer.byteLength(value) <= limit &&
      !/[\x00-\x1f\x7f]/.test(value) && Buffer.from(value).toString('utf8') === value;
    if (!text(input.report_id, OPERATION_REPORT_ID_BYTES) || !text(input.name, OPERATION_ARTIFACT_NAME_BYTES) || /[/\\]/.test(input.name) ||
        !Number.isSafeInteger(maxBytes) || maxBytes < 0 || typeof input.data !== 'string' && !(input.data instanceof Uint8Array)) throw new Error('Invalid operation upload declaration');
    const size = typeof input.data === 'string' ? Buffer.byteLength(input.data) : input.data.byteLength;
    if (size > maxBytes) throw new Error('Operation upload exceeds its application memory bound');
    const bytes = Buffer.from(input.data);
    const declaration: OperationArtifactUploadRequest = { report_id: input.report_id, name: input.name, size_bytes: size,
      sha256: 'sha256:' + createHash('sha256').update(bytes).digest('hex') };
    const fingerprint = JSON.stringify(declaration), prior = uploads.get(input.report_id);
    if (prior && prior.fingerprint !== fingerprint) throw new Error('Operation upload report ID already has a different declaration');
    if (prior?.artifact) return prior.artifact;
    if (prior?.pending) return prior.pending;
    const entry = prior ?? { fingerprint };
    const receipt = (value: { available: boolean; artifact?: OperationArtifact }): OperationArtifact | undefined => {
      if (typeof value?.available !== 'boolean' || value.available !== !!value.artifact) throw new OperationUploadProtocolError('Invalid operation upload receipt');
      const a = value.artifact;
      if (!a) return undefined;
      if (!uuid.test(a.id) || a.uri !== `operation://${id}/artifacts/${a.id}` || a.name !== declaration.name ||
          a.size_bytes !== declaration.size_bytes || a.sha256 !== declaration.sha256) throw new OperationUploadProtocolError('Operation upload receipt declaration changed');
      return Object.freeze({ id: a.id, name: a.name, uri: a.uri, size_bytes: a.size_bytes, sha256: a.sha256, ...(a.expires_at ? { expires_at: a.expires_at } : {}) });
    };
    entry.pending = (async () => {
      for (let n = 0; n < 3; n++) {
        await transport.checkpoint();
        try {
          const found = receipt(await transport.lookup(declaration));
          transport.guard();
          if (found) return found;
          const artifact = receipt(await transport.upload(declaration, Buffer.from(bytes)));
          transport.guard();
          if (!artifact) throw new OperationUploadProtocolError('Verified operation upload receipt required');
          return artifact;
        } catch (error) {
          if (transport.aborted() || error instanceof OperationUploadProtocolError || n === 2 || error instanceof OperationHTTPError && error.status < 500) throw error;
        }
      }
      throw new Error('Operation upload attempts exhausted');
    })().then(artifact => { entry.artifact = artifact; return artifact; }).finally(() => { entry.pending = undefined; });
    uploads.set(input.report_id, entry);
    return entry.pending;
  };
}
