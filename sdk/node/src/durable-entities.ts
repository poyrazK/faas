import { InvocationsService } from './generated/services/InvocationsService.js';
import type { DurableEntityInspectResponse } from './generated/models/DurableEntityInspectResponse.js';

export type DurableEntityInspectOptions = Parameters<typeof InvocationsService.inspectDurableEntity>[0];

/** Read metadata without acquiring ownership or invoking guest code. */
export async function inspectDurableEntity(options: DurableEntityInspectOptions): Promise<DurableEntityInspectResponse> {
  if (!options.namespace || !options.key) {
    throw new TypeError('durable entity inspection requires namespace and key');
  }
  const result = await InvocationsService.inspectDurableEntity(options);
  if (!Number.isSafeInteger(result.version) || result.version < 0) {
    throw new RangeError('durable entity version exceeds the safe integer range');
  }
  return result;
}

export type DurableEntityRetryOptions = Parameters<typeof InvocationsService.retryDurableEntity>[0];

/** Re-arm exhausted metadata. Inspect again after any uncertain response. */
export async function retryDurableEntity(options: DurableEntityRetryOptions) {
  const request = options.requestBody;
  if (!request.namespace || !request.key || !Number.isSafeInteger(request.expected_version)
    || request.expected_version <= 0 || !/^[0-9a-f]{64}$/.test(request.expected_recovery_revision)
    || (request.target !== 'alarm' && request.target !== 'outbox')
    || (request.target === 'alarm' && (!request.alarm_at || request.head_id !== undefined))
    || (request.target === 'outbox' && (!request.head_id || request.alarm_at !== undefined))) {
    throw new TypeError('durable entity retry requires a fresh inspection and exactly one recovery target');
  }
  const result = await InvocationsService.retryDurableEntity(options);
  if (!Number.isSafeInteger(result.version) || result.version <= 0) {
    throw new RangeError('durable entity version exceeds the safe integer range');
  }
  return result;
}

export type DurableEntityExportOptions = Parameters<typeof InvocationsService.exportDurableEntity>[0];
export type DurableEntityRestoreOptions = Parameters<typeof InvocationsService.restoreDurableEntity>[0];

/** Sensitive application data: store privately and preserve the complete envelope. */
export async function exportDurableEntity(options: DurableEntityExportOptions) {
  if (!options.namespace || !options.key) throw new TypeError('entity export requires namespace and key');
  const result = await InvocationsService.exportDurableEntity(options);
  if (!Number.isSafeInteger(result.version) || result.version <= 0) throw new RangeError('entity export version exceeds the safe integer range');
  return result;
}

/** Retry uncertain responses with this identical request ID and body. */
export async function restoreDurableEntity(options: DurableEntityRestoreOptions) {
  const r = options.requestBody;
  if (!r.namespace || !r.key || !r.request_id || !Number.isSafeInteger(r.expected_version) || r.expected_version <= 0
    || !Number.isSafeInteger(r.export.version) || r.export.version <= 0) throw new TypeError('entity restore requires selectors, stable ID and safe positive versions');
  const result = await InvocationsService.restoreDurableEntity(options);
  if (!Number.isSafeInteger(result.version) || result.version <= 0) throw new RangeError('restore response version is unsafe; the operation may have committed, retain the original request');
  return result;
}

export type DurableEntityBackupListOptions = Parameters<typeof InvocationsService.listDurableEntityBackups>[0];
export type DurableEntityBackupGetOptions = Parameters<typeof InvocationsService.getDurableEntityBackup>[0];
export type DurableEntityRestorePreviewOptions = Parameters<typeof InvocationsService.previewDurableEntityRestore>[0];

function safeStateVersion(value: number): void {
  if (!Number.isSafeInteger(value) || value <= 0) throw new RangeError('durable entity version exceeds the safe integer range');
}

export async function listDurableEntityBackups(options: DurableEntityBackupListOptions) {
  if (!options.namespace || !options.key) throw new TypeError('backup list requires namespace and key');
  const result = await InvocationsService.listDurableEntityBackups(options);
  for (const item of result.items) safeStateVersion(item.version);
  return result;
}

export async function getDurableEntityBackup(options: DurableEntityBackupGetOptions) {
  if (!options.namespace || !options.key || !options.backupId) throw new TypeError('backup read requires selectors and backup ID');
  const result = await InvocationsService.getDurableEntityBackup(options);
  safeStateVersion(result.export.version);
  return result;
}

/** Observational only; compatibility remains unverified and restore is fenced anew. */
export async function previewDurableEntityRestore(options: DurableEntityRestorePreviewOptions) {
  const r = options.requestBody;
  if (!r.namespace || !r.key) throw new TypeError('restore preview requires namespace and key');
  safeStateVersion(r.expected_version); safeStateVersion(r.export.version);
  const result = await InvocationsService.previewDurableEntityRestore(options);
  safeStateVersion(result.current_version); safeStateVersion(result.source_version);
  return result;
}

export type DurableEntityRestoreValidationOptions = Parameters<typeof InvocationsService.validateDurableEntityRestore>[0];
/** Executes a pure application validator; actual restore validates again. */
export async function validateDurableEntityRestore(options: DurableEntityRestoreValidationOptions) {
  const r = options.requestBody;
  if (!r.namespace || !r.key || !r.request_id) throw new TypeError('restore validation requires selectors and request ID');
  safeStateVersion(r.expected_version); safeStateVersion(r.export.version);
  const result = await InvocationsService.validateDurableEntityRestore(options);
  safeStateVersion(result.expected_version); safeStateVersion(result.source_version);
  return result;
}
