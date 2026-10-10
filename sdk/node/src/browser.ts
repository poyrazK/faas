// Browser-safe entry point. Keep this barrel free of imports from the Node
// SDK's root surface, which intentionally includes Node-only helpers.
export {
  createGregaleBrowserFetch,
  GREGALE_RELEASE_HEADER,
  GREGALE_RELEASE_COOKIE,
  GREGALE_RELEASE_SUBPROTOCOL_PREFIX,
  GREGALE_REVISION_HEADER,
  type GregaleBrowserFetchClient,
  type GregaleBrowserFetchOptions,
} from './browser-release.js';

export {
  consumeRealtimeChannel,
  consumeRealtimeChannels,
  consumeRealtimeInbox,
  RealtimeInboxResyncRequiredError,
  type ConsumeRealtimeInboxOptions,
  type RealtimeInboxConsumerOptions,
  type RealtimeInboxMessage,
  REALTIME_MAX_CHANNELS_PER_CONNECTION,
  REALTIME_RESUME_SUBPROTOCOL,
  RealtimeConfigurationError,
  RealtimeProtocolError,
  RealtimeResyncRequiredError,
  type ConsumeRealtimeChannelOptions,
  type ConsumeRealtimeChannelsOptions,
  type RealtimeChannelConsumerOptions,
  type RealtimeChannelActions,
  type RealtimeConnectionOptions,
  type RealtimeCursorStore,
  type RealtimeDirectMessage,
  type RealtimeEphemeralRejection,
  type RealtimeMessage,
  type RealtimePresenceEvent,
  type RealtimePresenceMember,
  type RealtimeSignal,
  type RealtimeReadProgress,
  type RealtimeReadActions,
  type RealtimePushRegistration,
  type RealtimeNotificationPreferences,
  type RealtimePushActions,
  realtimeWebPushRegistration,
  recoverRealtimeChannelSnapshot,
  publishRealtimeChannelBatch,
  realtimeEventSchemaMetadata,
  realtimeReducerEvent,
  realtimeExpectedSequence,
  type RealtimeReducerOperation, type RealtimeReducerCondition,
  realtimeScheduledEvent, type RealtimeScheduleRequest, type RealtimeSchedule, type RealtimeScheduleCondition,
  realtimeScheduleRetry, type RealtimeScheduleRetryRequest,
  type RealtimeScheduleHistory, type RealtimeScheduleHistoryEvent,
  type RealtimeScheduleCompletionWebhookPayload,
  realtimeActivityScopeChannel,
  createRealtimeSignalCoalescer, type RealtimeSignalCoalescer, type RealtimeSignalCoalescerOptions,
  realtimeBackendSignal, type RealtimeBackendSignalRequest, type RealtimeBackendSignalResponse,
  realtimeScheduleGroupRequest, type RealtimeScheduleGroupRequest, type RealtimeScheduleList, type RealtimeScheduleTotals,
  type RealtimeBatchRequest,
  type RealtimeBatchResult,
  type RealtimeChannelSnapshot,
  type RealtimeReadError,
  type RealtimeSocket,
} from './realtime-resume.js';
export {
  createBrowserRealtimeSocketFactory,
  REALTIME_RESUME_BEARER_SUBPROTOCOL_PREFIX,
  type BrowserRealtimeSocketFactory,
} from './browser-realtime.js';

export { GregaleOperationClient, OperationHTTPError, type Operation, type OperationClientOptions, type OperationList, type OperationListOptions, type OperationSummary, type OperationSubject, type OperationState, type OperationReceipt, type OperationEvents, type OperationReport, type OperationArtifactReport, type OperationMilestoneReport, type OperationMilestone, type OperationWorkflowState, type OperationWorkflowStateHistoryEntry, type OperationMilestones, type OperationMilestonePageOptions, type OperationBusinessMilestoneOptions, type OperationWorkflowOutcomeEntry, type OperationWorkflowOutcomesResponse, type OperationWorkflowOutcomeGroup, type OperationWorkflowOutcomeSummary, type OperationWorkflowOutcomeOptions, type OperationWorkflowOutcomeSummaryOptions, type OperationWorkflowDependency, type OperationWorkflowRelatedInstance, type OperationWorkflowDependencyImpact, type OperationWorkflowDependencyTrace, type OperationWorkflowReadinessRequest, type OperationWorkflowTransitionReadiness, type OperationWorkflowReadinessResponse, type OperationWorkflowReadinessOverview, type OperationWorkflowDependencyFinding, type OperationWorkflowDependentInstance, type OperationWorkflowAttentionEntry, type OperationWorkflowAttentionResponse, type OperationWorkflowAttentionOptions, type OperationWorkflowAttentionStats, type OperationWorkflowAttentionGroup, type OperationWorkflowAttentionSummary, type OperationWorkflowAttentionSummaryOptions, type OperationWorkflowBlockerResolution, type OperationWorkflowBlocker, type OperationWorkflowDecision, type OperationWorkflowInstanceSnapshot, type OperationWorkflowInstanceTransition, type OperationWorkflowInstanceStep, type OperationWorkflowInstanceMilestoneRef } from './customer-operations.js';
export { GregaleOperationSession, type OperationSessionOptions, type OperationSessionClient, type OperationSessionUpdate } from './operation-session.js';
export { CustomerOperationAuth, type CustomerOperationAuthOptions, type CustomerOperationAuthProvider } from './operation-auth.js';
export { CustomerOperationFeature, type CustomerOperationFeatureConnection, type CustomerOperationFeatureOptions } from './operation-feature.js';
export { createBrowserOperationReceiptStore, type OperationReceiptStore, type OperationReceiptAccess, type SavedOperationSubmission, type OperationReceiptScope, type BrowserOperationReceiptStoreOptions, type OperationSubmissionResume } from './operation-submission.js';
export type { OperationTenantIdentity, OperationSubmissionScope, OperationSubmissionFence, OperationSubmissionLookup, OperationSubmissionLookupOptions, OperationWorkflowPolicyRequirement, OperationWorkflowPlannedDecision } from './customer-operations.js';
export type { OperationWorkflowActionPreviewRequest } from './generated/models/OperationWorkflowActionPreviewRequest.js';
export type { OperationWorkflowActionPreviewResponse } from './generated/models/OperationWorkflowActionPreviewResponse.js';
export type { OperationWorkflowInvariantRequirement } from './generated/models/OperationWorkflowInvariantRequirement.js';
export type { OperationWorkflowPlannedInvariant } from './generated/models/OperationWorkflowPlannedInvariant.js';
export type { OperationWorkflowUnmetInvariant } from './generated/models/OperationWorkflowUnmetInvariant.js';
export type { OperationWorkflowEffectRequirement } from './generated/models/OperationWorkflowEffectRequirement.js';
export type { OperationWorkflowPlannedEffect } from './generated/models/OperationWorkflowPlannedEffect.js';
export type { OperationWorkflowUnmetEffect } from './generated/models/OperationWorkflowUnmetEffect.js';
export type { OperationBusinessEffectReference } from './generated/models/OperationBusinessEffectReference.js';
export type { OperationBusinessCompensation } from './generated/models/OperationBusinessCompensation.js';
export type { OperationBusinessCompensationPayload } from './generated/models/OperationBusinessCompensationPayload.js';

export {
  createRealtimeSignalTracker,
  type RealtimeSignalTracker,
  type RealtimeTemporarySignal,
} from './realtime-signals.js';

export {
  createRealtimeActivityTracker,
  type RealtimeActivity,
  type RealtimeActivityTracker,
  type RealtimeActivityTrackerOptions,
} from './realtime-activity.js';

export {
  aggregateRealtimeActivity, formatRealtimeTypingSummary, createRealtimePresenceDirectory,
  type RealtimeActivityParticipant, type RealtimeActivitySummary, type RealtimeActivitySummaryOptions,
  type RealtimePresenceDirectory, type RealtimePresenceDirectoryOptions,
} from './realtime-activity-summary.js';
