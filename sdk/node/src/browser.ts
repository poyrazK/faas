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
export {
  createBrowserRealtimeSocketFactory,
  REALTIME_RESUME_BEARER_SUBPROTOCOL_PREFIX,
  type BrowserRealtimeSocketFactory,
} from './browser-realtime.js';

export { GregaleOperationClient, OperationHTTPError, type Operation, type OperationClientOptions, type OperationList, type OperationListOptions, type OperationSummary, type OperationState, type OperationReceipt, type OperationEvents, type OperationReport, type OperationArtifactReport } from './customer-operations.js';
export { GregaleOperationSession, type OperationSessionOptions, type OperationSessionClient, type OperationSessionUpdate } from './operation-session.js';
export { CustomerOperationAuth, type CustomerOperationAuthOptions, type CustomerOperationAuthProvider } from './operation-auth.js';
export { CustomerOperationFeature, type CustomerOperationFeatureConnection, type CustomerOperationFeatureOptions } from './operation-feature.js';

export { createBrowserOperationReceiptStore, type OperationReceiptStore, type OperationReceiptAccess, type SavedOperationSubmission, type OperationReceiptScope, type BrowserOperationReceiptStoreOptions, type OperationSubmissionResume } from './operation-submission.js';
export type { OperationTenantIdentity, OperationSubmissionScope, OperationSubmissionFence, OperationSubmissionLookup, OperationSubmissionLookupOptions } from './customer-operations.js';
