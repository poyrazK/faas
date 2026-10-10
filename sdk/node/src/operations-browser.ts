// Dedicated entry point for customer feature code. Keep managed transaction
// helpers and workload reporting authority out of this browser module graph.
export { GregaleOperationClient, OperationHTTPError } from './customer-operations.js';
export type {
  Operation, OperationState, OperationProgress, OperationDelivery, OperationArtifact,
  OperationArtifactReport, OperationReceipt, OperationSummary, OperationList,
  OperationListOptions, OperationEvent, OperationEvents, OperationReport, OperationClientOptions,
} from './customer-operations.js';
export { GregaleOperationSession } from './operation-session.js';
export type { OperationSessionClient, OperationSessionOptions, OperationSessionUpdate } from './operation-session.js';
export { CustomerOperationAuth } from './operation-auth.js';
export type { CustomerOperationAuthOptions, CustomerOperationAuthProvider } from './operation-auth.js';
export { CustomerOperationFeature } from './operation-feature.js';
export type { CustomerOperationFeatureConnection, CustomerOperationFeatureOptions } from './operation-feature.js';

export { createBrowserOperationReceiptStore, type OperationReceiptStore, type OperationReceiptAccess, type SavedOperationSubmission, type OperationReceiptScope, type BrowserOperationReceiptStoreOptions, type OperationSubmissionResume } from './operation-submission.js';
export type { OperationTenantIdentity, OperationSubmissionScope, OperationSubmissionFence, OperationSubmissionLookup, OperationSubmissionLookupOptions } from './customer-operations.js';
