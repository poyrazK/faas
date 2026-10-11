// ADR-938: scope-bound clients. Uses the existing FaaSClient global configuration.
import { InvocationsService } from './generated/services/InvocationsService.js';
import { inspectDurableEntity, retryDurableEntity } from './durable-entities.js';
import { durableEntityJSON } from './durable-entity-handler.js';
import type { DurableEntityRetryRequest } from './generated/models/DurableEntityRetryRequest.js';

export interface DurableEntityHandleScope {
  slug: string;
  namespace: string;
  key: string;
  environment?: string;
  platformTenantId?: string;
}

export interface DurableEntityResult<Result> { value: Result; version: number; replayed: boolean }

/** A result decode failure can follow a successful commit. Reuse the replay ID. */
export class DurableEntityResultDecodeError extends Error {
  constructor(public readonly version: number, public readonly replayed: boolean, cause: unknown) {
    super('failed to decode committed entity result', { cause });
    this.name = 'DurableEntityResultDecodeError';
  }
}

export function durableEntityHandle<Payload, Result>(scope: DurableEntityHandleScope, decodeResult: (value: unknown) => Result) {
  const bound = Object.freeze({ ...scope });
  if (!bound.slug || !bound.namespace || !bound.key) throw new TypeError('entity handle requires app, namespace and key');
  const selectors = Object.freeze({ namespace: bound.namespace, key: bound.key, environment: bound.environment, platform_tenant_id: bound.platformTenantId });
  return Object.freeze({
    async invoke(requestId: string, payload: Payload): Promise<DurableEntityResult<Result>> {
      if (!requestId) throw new TypeError('entity request_id is required');
      // Snapshot strict JSON before any asynchronous transport/retry can observe caller mutations.
      const snapshot: unknown = JSON.parse(durableEntityJSON(payload));
      const result = await InvocationsService.invokeDurableEntity({ slug: bound.slug, requestBody: { ...selectors, request_id: requestId, payload: snapshot } });
      if (!Number.isSafeInteger(result.version) || result.version <= 0) throw new RangeError('durable entity version exceeds the safe integer range');
      try { return { value: decodeResult(result.value as unknown), version: result.version, replayed: result.replayed }; }
      catch (cause) { throw new DurableEntityResultDecodeError(result.version, result.replayed, cause); }
    },
    inspect() { return inspectDurableEntity(bound); },
    retry(request: Omit<DurableEntityRetryRequest, 'namespace' | 'key' | 'environment' | 'platform_tenant_id'>) {
      return retryDurableEntity({ slug: bound.slug, requestBody: { ...request, ...selectors } });
    },
  });
}
