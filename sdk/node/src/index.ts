// src/index.ts — public barrel for the Node SDK.
//
// ADR-20261023115741: this default entry exposes generally available and
// beta capabilities only: the client generated from sdk/openapi.yaml (the GA
// and beta subset of api/openapi.yaml, `make sdk-spec`) and the hand-written
// helpers for those capabilities. Preview and internal capabilities are in
// '@gregale/sdk-node/preview' (src/preview.ts), which re-exports everything.
//
// The generated services call `OpenAPI.BASE/TOKEN/HEADERS` from the shared
// runtime core in `src/generated/core/` — the `FaaSClient` constructor sets
// these once at construction time. Mutating methods receive a fresh
// `Idempotency-Key` per logical call, preserved across retries, via the
// fetch wrapper stack installed on `globalThis.fetch` (see `client.ts`).

// Generated runtime core, shared with the preview entry.
export {
  ApiError,
  CancelablePromise,
  CancelError,
  OpenAPI,
} from './generated/index.js';
export type { OpenAPIConfig } from './generated/index.js';

// Generated services: one class per OpenAPI tag with a GA or beta operation.
export * from './generated-public/services/index.js';

// Generated models for GA and beta operations.
export type * from './generated-public/models/index.js';

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
// SSE streaming helpers.
export { streamSse, parseFrame, type SseEvent } from './sse.js';
// Request-scoped release propagation for app-to-app calls.
export {
  createGregaleFetch,
  currentGregaleRelease,
  gregaleReleaseMetaTag,
  withGregaleReleaseContext,
  withGregaleRequestContext,
  GREGALE_RELEASE_HEADER,
  GREGALE_REVISION_HEADER,
} from './release-context.js';
// Server-side verification of incoming service-binding identity assertions.
export {
  createServiceCallerVerifier,
  ServiceCallerVerificationError,
  SERVICE_CALLER_ASSERTION_HEADER,
  type ServiceCallerErrorCode,
  type ServiceCallerVerifier,
  type ServiceCallerVerifierOptions,
  type VerifiedServiceCaller,
} from './servicecaller.js';
// Outbound webhook receiver verification (Node-only; not re-exported by ./browser).
export {
  verifyWebhook,
  WebhookVerificationError,
  WEBHOOK_SIGNATURE_HEADER,
  WEBHOOK_TIMESTAMP_HEADER,
  WEBHOOK_DELIVERY_ID_HEADER,
  DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE_MS,
  type WebhookHeaders,
  type WebhookVerificationErrorCode,
  type VerifiedWebhook,
  type VerifyWebhookOptions,
} from './webhook.js';
// Opaque login-target signal for opt-in pre-auth abuse observation.
export { preAuthTargetDigest, PRE_AUTH_TARGET_HEADER } from './pre-auth-target.js';
