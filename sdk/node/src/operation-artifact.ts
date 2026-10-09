import { createHash, randomUUID } from 'node:crypto';
import type { Operation, OperationArtifactReport } from './customer-operations.js';

export interface OperationArtifactInput {
  name: string;
  /** Managed obj://app-uuid/bucket-uuid/key reference; never a signed URL. */
  uri: string;
  data: string | Uint8Array;
  /** Application memory bound. The API independently enforces plan limits. */
  maxBytes: number;
  report_id?: string;
}

export interface OperationArtifactUpload {
  /** Cooperative cancellation, when the request uses a cancellable scope. */
  readonly signal?: AbortSignal;
  readonly report: Readonly<OperationArtifactReport>;
  /** A copy of the prepared bytes, for the application's existing bucket writer. */
  readonly bytes: Uint8Array;
}

export interface PreparedOperationArtifact<T = Operation> {
  readonly report: Readonly<OperationArtifactReport>;
  /** Write once, then attach. Later calls only replay the attachment report,
   * including after an uncertain write; they never automatically write again. */
  uploadAndAttach(write: (upload: OperationArtifactUpload) => Promise<void>): Promise<T>;
  /** Attach an existing source or reconcile a lost upload/attachment response.
   * Gregale verifies the source before retaining it, or replays a prior receipt. */
  attach(): Promise<T>;
}

/** @internal The runtime supplies a guard bound to the original request. */
export function prepareOperationArtifact<T = Operation>(input: OperationArtifactInput, guard: () => void,
  attach: (report: OperationArtifactReport) => Promise<T>,
  reuse?: (report: OperationArtifactReport) => Promise<T | undefined>,
  signal?: () => AbortSignal | undefined): PreparedOperationArtifact<T> {
  guard();
  if (!Number.isSafeInteger(input.maxBytes) || input.maxBytes < 0) throw new Error('A nonnegative artifact memory bound is required');
  if (typeof input.data !== 'string' && !(input.data instanceof Uint8Array)) throw new Error('Artifact data must be text or bytes');
  const size = typeof input.data === 'string' ? Buffer.byteLength(input.data, 'utf8') : input.data.byteLength;
  if (size > input.maxBytes) throw new Error('Artifact exceeds the application memory bound');
  // Wire bounds mirror pkg/api/limits.go; no plan quota is decided in the SDK.
  const validText = (value: string, max: number): boolean => typeof value === 'string' && value.length > 0 &&
    Buffer.byteLength(value, 'utf8') <= max && !/[\x00-\x1f\x7f]/.test(value) && Buffer.from(value, 'utf8').toString('utf8') === value;
  const reportID = input.report_id ?? randomUUID();
  if (!validText(input.name, 128) || /[/\\]/.test(input.name) || !validText(reportID, 128)) throw new Error('Invalid artifact name or report ID');
  // Preserve opaque keys (including dot segments); URL construction would
  // normalize a source key and could attach a different object.
  const uuid = '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}';
  const reference = typeof input.uri === 'string' ? new RegExp(`^obj://${uuid}/${uuid}/([^%?#]+)$`).exec(input.uri) : null;
  if (!validText(input.uri, 2048) || !reference || !validText(reference[1]!, 1024)) throw new Error('A managed artifact object reference is required');
  const bytes = typeof input.data === 'string' ? Buffer.from(input.data, 'utf8') : Buffer.from(input.data.subarray(0, size));
  const report: Readonly<OperationArtifactReport> = Object.freeze({ report_id: reportID, name: input.name, uri: input.uri,
    size_bytes: bytes.length, sha256: 'sha256:' + createHash('sha256').update(bytes).digest('hex') });
  let sourceAttempted = false, pending: Promise<T> | undefined, attached: T | undefined;

  function run(write?: (upload: OperationArtifactUpload) => Promise<void>): Promise<T> {
    try {
      guard();
      if (pending) return pending;
      if (attached) return Promise.resolve(attached);
      const upload = write && !sourceAttempted;
      if (!reuse) sourceAttempted = true;
      // Register the pending promise before calling application code, which
      // may synchronously ask for the same attachment from an upload hook.
      pending = Promise.resolve().then(async () => {
        guard();
        if (reuse) {
          const receipt = await reuse({ ...report });
          guard();
          if (receipt !== undefined) { attached = receipt; return receipt; }
        }
        if (upload) {
          sourceAttempted = true;
          const stop = signal?.();
          await write!({ report, bytes: Uint8Array.from(bytes), ...(stop ? { signal: stop } : {}) });
        }
        guard();
        const response = await attach({ ...report });
        guard();
        attached = response;
        return attached;
      }).finally(() => { pending = undefined; });
      return pending;
    } catch (error) { return Promise.reject(error); }
  }

  return Object.freeze({ report, uploadAndAttach: (write: (upload: OperationArtifactUpload) => Promise<void>) =>
    typeof write === 'function' ? run(write) : Promise.reject(new Error('An artifact bucket writer is required')), attach: () => run() });
}
