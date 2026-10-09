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
export { AlertRulesService } from './generated/services/AlertRulesService.js';
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
export { ManagedPostgresService } from './generated/services/ManagedPostgresService.js';
export { MetaService } from './generated/services/MetaService.js';
export { MfaService } from './generated/services/MfaService.js';
export { OperationsService } from './generated/services/OperationsService.js';
export { OutboundService } from './generated/services/OutboundService.js';
export { QueuesService } from './generated/services/QueuesService.js';
export { TriggersService } from './generated/services/TriggersService.js';
export { ProjectsService } from './generated/services/ProjectsService.js';
export { RealtimeService } from './generated/services/RealtimeService.js';
export { RunsService } from './generated/services/RunsService.js';
export { SecretsService } from './generated/services/SecretsService.js';
export { UsageService } from './generated/services/UsageService.js';
export { WebhooksService } from './generated/services/WebhooksService.js';
export { WorkflowsService } from './generated/services/WorkflowsService.js';
export { InboundWebhooksService } from './generated/services/InboundWebhooksService.js';

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

// SSE streaming helpers.
export { streamSse, parseFrame, type SseEvent } from './sse.js';
export {
  runExecution,
  watchExecution,
  type ExecutionEvent,
  type ExecutionEventData,
  type ExecutionEventType,
  type RunExecutionOptions,
  type WatchExecutionOptions,
} from './executions.js';

export {
  consumeRealtimeChannel,
  consumeRealtimeChannels,
  REALTIME_MAX_CHANNELS_PER_CONNECTION,
  REALTIME_RESUME_SUBPROTOCOL,
  RealtimeConfigurationError,
  RealtimeProtocolError,
  RealtimeResyncRequiredError,
  type ConsumeRealtimeChannelOptions,
  type ConsumeRealtimeChannelsOptions,
  type RealtimeChannelConsumerOptions,
  type RealtimeConnectionOptions,
  type RealtimeCursorStore,
  type RealtimeMessage,
  type RealtimeSocket,
} from './realtime-resume.js';

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

export { createIssueReporter, type IssueContext, type IssueReporterOptions } from './issues.js';

export { GregaleFlags, evaluateFlag, evaluateVariant, flagBucket, flagVariantBucket, flagSubjectBucket, flagSubjectVariantBucket, validFlagSubjectID, GREGALE_FLAG_EVIDENCE_HEADER, GREGALE_FLAG_CONTEXT_HEADER, GREGALE_FLAG_PROPAGATION_HEADER } from './flags.js';
export type { FlagsBundle, FlagRule, VariantFlagRule, ProgressiveRollout, FeatureFlag, FlagDefinition, BooleanFeatureFlag, VariantFeatureFlag, WeightedVariant, BooleanFlagDecision, VariantFlagDecision, AnyFlagDecision, FlagDecision, FlagEvidence, VariantFlagEvidence, AnyFlagEvidence, FlagRequestHeaders, FlagDecisionOrigin, GregaleFlagsOptions } from './flags.js';

export { FlagsService } from './generated/services/FlagsService.js';

export { DevService } from './generated/services/DevService.js';
export {
  DEV_BRIDGE_CONTEXT_HEADER,
  createDevBridgeFetch,
  currentDevBridgeContext,
  devBridgeMiddleware,
  withDevBridgeContext,
  withDevBridgeRequestContext,
} from './dev-bridge.js';

export { decodeExecutionArtifact } from './execution-artifacts.js';

export { GregaleOperationClient, OperationHTTPError, type Operation, type OperationClientOptions, type OperationList, type OperationListOptions, type OperationSummary, type OperationSubject, type OperationState, type OperationReceipt, type OperationEvents, type OperationReport, type OperationArtifactReport, type OperationMilestoneReport, type OperationMilestone, type OperationWorkflowState, type OperationMilestones, type OperationMilestonePageOptions, type OperationBusinessMilestoneOptions } from './customer-operations.js';
export { GregaleOperations, type GregaleOperationsOptions, type OperationExecutionContext } from './operations-runtime.js';
export {
  decodeDurableEntityHandlerRequest, durableEntityWebhookIntent, encodeDurableEntityTransition,
  type DurableEntityIdentity, type DurableEntityHandlerLimits, type DurableEntityHandlerRequest,
  type DurableEntityWebhookIntent, type DurableEntityTransition,
} from './durable-entity-handler.js';
export {
  DURABLE_ENTITY_PROTOCOL_VERSION, DURABLE_ENTITY_OUTBOX_PROTOCOL_VERSION,
  DURABLE_ENTITY_MAX_REQUEST_BYTES, DURABLE_ENTITY_MAX_TRANSITION_BYTES, DURABLE_ENTITY_HANDLER_PATH,
  DURABLE_ENTITY_RESTORE_VALIDATION_PROTOCOL_VERSION, DURABLE_ENTITY_RESTORE_VALIDATION_PATH, DURABLE_ENTITY_RESTORE_VALIDATION_MAX_RESPONSE_BYTES,
} from './durable-entity-contract.js';

export { insertCommitEvent, type CommitEvent, type CommitEventRouting, type CommitTransaction } from "./commit.js";

export { operationReceiptSchema, customerOperationReceiptSchema } from "./operation-contract.js";
export { customerOperationRequestFromHeaders, customerOperationRequestDigest, withCustomerOperationTransaction,
  type CustomerOperationHTTPRequest, type CustomerOperationTransactionRequest } from './customer-operation-transactions.js';
export {
  operationRequestFromHeaders,
  operationRequestDigest,
  withOperationTransaction,
  OperationConflictError,
  OperationCommitUnknownError,
  type OperationRequest,
  type OperationOutcome,
  type OperationTransaction,
  type OperationConnection,
  type OperationPool,
  type OperationTransactionResult,
} from "./operations.js";

export { OperationMilestonePublicationError, type CustomerOperationTransaction } from './customer-operation-milestones.js';
export { OperationWorkflowStatePublicationError, type OperationWorkflowStateReport, type OperationWorkflowStateReceipt } from './customer-operation-workflow-states.js';

export { inspectDurableEntity, type DurableEntityInspectOptions } from './durable-entities.js';
export { retryDurableEntity, type DurableEntityRetryOptions } from './durable-entities.js';
export { exportDurableEntity, restoreDurableEntity, type DurableEntityExportOptions, type DurableEntityRestoreOptions } from './durable-entities.js';
export { listDurableEntityBackups, getDurableEntityBackup, previewDurableEntityRestore, type DurableEntityBackupListOptions, type DurableEntityBackupGetOptions, type DurableEntityRestorePreviewOptions } from './durable-entities.js';
export { decodeDurableEntityRestoreValidationRequest, encodeDurableEntityRestoreValidation, type DurableEntityRestoreValidationRequest } from './durable-entity-restore-validation.js';
export { validateDurableEntityRestore, type DurableEntityRestoreValidationOptions } from './durable-entities.js';

export { durableEntityHandle, DurableEntityResultDecodeError, type DurableEntityHandleScope, type DurableEntityResult } from './durable-entity-handle.js';
export { decodeDurableEntityCall, DurableEntityTransitionBuilder, type DurableEntityCall, type DurableEntityCallOptions } from './durable-entity-call.js';
export { decodeDurableEntitySchemaCall, type DurableEntityStateSchema, type DurableEntitySchemaState, type DurableEntitySchemaCall } from './durable-entity-schema.js';
