import { operationRequestDigest, type OperationRequest } from './operations.js';
import type { OperationTransaction } from './operation-receipt.js';
import { customerOperationRequestDigest, type CustomerOperationTransactionRequest } from './customer-operation-transactions.js';
import {captureReadinessProbe as repairLimits} from './data-repair-readiness-probe.js';

export interface DataRepairCapture {
  table: string;
  environment: string;
  release: string;
}

/** Call inside a managed or Customer Operation's transaction callback. The
 * installed SQL schema captures actual updates. This helper owns no transaction,
 * performs no DDL, and never authorizes a caller from untrusted HTTP headers. */
export async function withDataRepairCapture<T>(
  transaction: OperationTransaction, request: OperationRequest | CustomerOperationTransactionRequest, config: DataRepairCapture,
  handler: (transaction: OperationTransaction) => Promise<T>,
): Promise<T> {
  // Both supported receipt protocols validate their private negotiation brand.
  if ('generation' in request) operationRequestDigest(request);
  else customerOperationRequestDigest(request);
  if (![[config.table,repairLimits.max_table_bytes],[config.environment,repairLimits.max_context_bytes],[config.release,repairLimits.max_context_bytes]].every(([value,limit]) =>
    typeof value === 'string' && value.length > 0 && Buffer.byteLength(value) <= Number(limit) && !value.includes('\0') && Buffer.from(value).toString() === value)) {
    throw new TypeError('invalid data repair capture configuration');
  }
  const scope = {
    account_id: request.accountId.toLowerCase(), app_id: request.appId.toLowerCase(), environment: config.environment,
    ...(request.platformTenantId ? { platform_tenant_id: request.platformTenantId.toLowerCase() } : {}),
  };
  await transaction.query('SELECT gregale_repair.begin_operation($1::uuid,$2::jsonb,$3,$4::regclass)',
    [request.operationId.toLowerCase(), JSON.stringify(scope), config.release, config.table]);
  const result = await handler(transaction);
  await transaction.query('SELECT gregale_repair.finish_operation($1::uuid)', [request.operationId.toLowerCase()]);
  return result;
}
