// src/index.ts — public barrel for the Node SDK.
//
// Re-exports the generated client surface (services + models + core
// helpers) and the hand-written wrapper façade (`FaaSClient` +
// sentinels + idempotency + SSE). Customers `import { ... } from
// '@gregale/sdk-node'` and never touch `src/generated/` directly.
//
// The generated services call `OpenAPI.BASE/TOKEN/HEADERS` from
// `src/generated/core/OpenAPI.ts` — the `FaaSClient` constructor
// sets these once at construction time. Mutating methods receive a
// fresh `Idempotency-Key` per attempt via the fetch wrapper stack
// installed on `globalThis.fetch` (see `client.ts`).

// Generated surface.
export {
  ApiError,
  CancelablePromise,
  CancelError,
  OpenAPI,
} from './generated/index.js';
export type { OpenAPIConfig } from './generated/index.js';

// Generated services (one class per OpenAPI tag).
export { AccountService } from './generated/services/AccountService.js';
export { AppsService } from './generated/services/AppsService.js';
export { AuditService } from './generated/services/AuditService.js';
export { AuthService } from './generated/services/AuthService.js';
export { CronsService } from './generated/services/CronsService.js';
export { DelayedTasksService } from './generated/services/DelayedTasksService.js';
export { DeploymentsService } from './generated/services/DeploymentsService.js';
export { DomainsService } from './generated/services/DomainsService.js';
export { GithubService } from './generated/services/GithubService.js';
export { InstancesService } from './generated/services/InstancesService.js';
export { InvocationsService } from './generated/services/InvocationsService.js';
export { KeysService } from './generated/services/KeysService.js';
export { MetaService } from './generated/services/MetaService.js';
export { MfaService } from './generated/services/MfaService.js';
export { QueuesService } from './generated/services/QueuesService.js';
export { SecretsService } from './generated/services/SecretsService.js';
export { UsageService } from './generated/services/UsageService.js';

// Generated models (one type per OpenAPI schema).
export type * from './generated/models/index.js';

// Hand-written wrapper façade.
export {
  FaaSClient,
  type FaaSClientOptions,
  type RetryPolicy,
  type SdkLogger,
} from './client.js';

// Error sentinels + helpers.
export {
  ErrNotFound,
  ErrUnauthorized,
  ErrRateLimited,
  ErrCapacity,
  problemToError,
  asFaasError,
  isFaasError,
  type FaasError,
  type Problem,
} from './errors.js';

// Idempotency helpers.
export {
  MUTATING_METHODS,
  isMutating,
  mintIdempotencyKey,
  type IdempotencyKey,
} from './idempotency.js';

// SSE streaming (the OpenAPI spec has no SSE endpoints today, but
// `/v1/logs/{app_id}/tail` and friends are out-of-spec SSE streams
// the SDK supports via `streamSse`).
export { streamSse, parseFrame, type SseEvent } from './sse.js';