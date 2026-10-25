package state_test

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// contractTestStore is the store the MemStore/PgStore contract tests in this package run
// against: state.Store plus every optional store interface and method they use. Contract helpers
// name it through an alias (type fooTestStore = contractTestStore) instead of declaring their own
// Store-sized interface. The compiler generates a wrapper for each method of each interface type
// a package declares and converts to, so the 66 Store-sized interfaces this replaced made this
// test compile peak at 4.9 GB of heap instead of 1.5 GB (ADR-982).
type contractTestStore interface {
	state.Store // teststorecheck:allow the one Store-sized interface the contracts alias
	cloneFlagFixtureStore
	projectEnvironmentQualificationStore
	state.AccountAbuseHoldStore
	state.AppWorkPolicyStore
	state.AuditEventOutboxStore
	state.ComputeNodeCPUUsageBatcher
	state.ComputeNodeUsageBatcher
	state.DeploymentAliasStore
	state.DeploymentHostingFailureStore
	state.DeploymentHostingReceiptStore
	state.DeploymentWorkloadSpecReader
	state.DevSourcePatchStore
	state.DevWatchStore
	state.EnvironmentWorkCancellationStore
	state.EventFanoutAttemptHistoryStore
	state.EventFanoutReplayBatchStore
	state.EventFanoutReplayStore
	state.EventReceiptAcceptanceStore
	state.EventReceiptReplayStore
	state.EventReceiptStore
	state.EventReplayPreviewStore
	state.EventStorageUsageStore
	state.EventSubscriptionControlStore
	state.EventSubscriptionStore
	state.EventWorkBindingStore
	state.FeatureFlagStore
	state.HostPortLeaseStore
	state.InvocationEnvironmentQueueAdmissionReader
	state.InvocationWorkEnvironmentAdmissionReader
	state.ManagedRealtimeConditionalStore
	state.ManagedRealtimeDirectMessageReceiptStore
	state.ManagedRealtimeEndpointStore
	state.ManagedRealtimeEventSchemaStore
	state.ManagedRealtimeHistoryStore
	state.ManagedRealtimeInboxStore
	state.ManagedRealtimeMutationStore
	state.ManagedRealtimeNotificationControlStore
	state.ManagedRealtimeNotificationStore
	state.ManagedRealtimeNotificationTimelineStore
	state.ManagedRealtimePresenceStore
	state.ManagedRealtimePushPreferencesStore
	state.ManagedRealtimePushStore
	state.ManagedRealtimeReadProgressStore
	state.ManagedRealtimeReducerStore
	state.ManagedRealtimeScheduleGroupStore
	state.ManagedRealtimeScheduleHistoryStore
	state.ManagedRealtimeScheduleStore
	state.ManagedRealtimeSnapshotStore
	state.MigrationAdmissionStore
	state.MigrationCommitRecoveryStore
	state.MigrationLeaseStore
	state.MigrationSourceStore
	state.NodeLeaseStore
	state.NodeLifecycleCompletionStore
	state.ObjectBucketAccessStore
	state.ObjectBucketReservationResultStore
	state.ObjectBucketStore
	state.ObjectBucketWriteFenceStore
	state.ObjectCapacityStore
	state.ObjectMultipartCompletionStore
	state.ObjectMultipartTransferStore
	state.ObjectMultipartURLCapabilityStore
	state.ObjectMultipartUploadStore
	state.ObjectNodeSettlementStore
	state.ObjectPreparedUploadFailureStore
	state.ObjectS3CredentialBindingStore
	state.ObjectStorageAccountingStore
	state.ObjectStorageCustomerUsageV2Store
	state.ObjectStorageGatewayEgressStore
	state.ObjectStorageGatewayRequestStore
	state.ObjectStorageProviderEgressStore
	state.ObjectStorageProviderUsageStore
	state.ObjectTrackedGatewayCopyStore
	state.ObjectTrackedGatewayUploadStore
	state.ObjectTrackedUploadStore
	state.ObjectURLCapabilityStore
	state.ObjectUploadRouteStore
	state.ObjectWriteReceiptStore
	state.OperatorColdBootStore
	state.OperatorNodeStore
	state.OperatorParkStore
	state.OperatorRecoverySubmissionStore
	state.OperatorRestartStore
	state.PlatformTenantReconciliationStore
	state.PlatformTenantStore
	state.PlatformTenantWebhookStore
	state.ProfileCaptureQueue
	state.ProfileCaptureStore
	state.ProjectEnvironmentCloneBindingCaptureStore
	state.ProjectEnvironmentCloneLeasedObjectManifestStore
	state.ProjectEnvironmentCloneObjectBucketStore
	state.ProjectEnvironmentCloneObjectCredentialStore
	state.ProjectEnvironmentCloneObjectManifestStore
	state.ProjectEnvironmentCloneObjectWriteFenceStore
	state.ProjectEnvironmentCloneOperationStore
	state.ProjectEnvironmentClonePublicationStore
	state.ProjectEnvironmentCloneValuesStore
	state.ProjectEnvironmentCloneWorkerLeaseStore
	state.ProjectEnvironmentCloneWorkloadStore
	state.ProjectEnvironmentPromotionFeatureFlagStore
	state.ProjectEnvironmentPromotionReleaseSetStore
	state.ProjectEnvironmentPromotionWorkloadSpecStore
	state.ProjectEnvironmentQualificationStore
	state.ProjectEnvironmentQueueConsumerStore
	state.ProjectEnvironmentQueueDeliveryStore
	state.ProjectEnvironmentQueueInvocationStore
	state.ProjectEnvironmentSecretStore
	state.ProjectEnvironmentWorkPolicyStore
	state.ProjectEnvironmentWorkloadQualificationStore
	state.ProjectEnvironmentWorkloadSpecStore
	state.ProjectPromotionDeploymentStore
	state.ProjectReconcileStore
	state.ProjectReleaseSetReader
	state.ProjectReleaseSetStore
	state.PublishedEventAdoptionBatchClaimStore
	state.PublishedEventRecipientAdmissionStore
	state.PublishedEventRecipientAdoptionClaimStore
	state.PublishedEventRecipientBatchClaimStore
	state.PublishedEventRecipientProgressStore
	state.PublishedEventRecipientWorkStore
	state.PublishedEventRetentionStore
	state.PublishedEventWorkStore
	state.ReleaseReviewStore
	state.RuntimeAppEnvStore
	state.RuntimeConfigMigrationStore
	state.SnapshotLocalityStore
	state.SnapshotOriginStore
	state.SnapshotRecoveryStore
	state.SnapshotReplicaLeaseStore
	state.WarmPoolReconciliationStore
	state.WorkCancellationStore

	AppRuntimeConfigChangedAt(context.Context, string) (time.Time, bool, error)
	AppendEventAt(context.Context, string, string, *string, []byte, time.Time) error
	CloneProjectEnvironment(context.Context, state.ProjectEnvironmentClone, api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error)
	CreateAPIKey(context.Context, string, []byte, string, []string) (state.APIKey, error)
	CreateManagedRealtimeEndpointIfUnderQuota(context.Context, state.ManagedRealtimeEndpoint, int, int) (state.ManagedRealtimeEndpoint, error)
	CreateSnapshot(context.Context, state.Snapshot) (state.Snapshot, error)
	DeleteManagedObjectStorageSecrets(context.Context, string) error
	DeleteManagedRealtimeEndpoint(context.Context, string) error
	DeploymentSidecarSecretReloadSignal(context.Context, string, string) (string, error)
	GetAppSecretInScope(context.Context, string, string, string, string) (*state.AppSecret, error)
	GetProjectEnvironmentEdgePolicy(context.Context, string, string, string) (state.ProjectEnvironmentEdgePolicy, error)
	GetProjectEnvironmentRoutePolicy(context.Context, string, string, string) (state.ProjectEnvironmentRoutePolicy, error)
	LatestSnapshot(context.Context, string) (state.Snapshot, error)
	ListDueInvocationsAfter(context.Context, time.Time, state.InvocationDueCursor, int) ([]state.Invocation, error)
	LiveDeploymentForScope(context.Context, string, string) (state.Deployment, error)
	ProjectEnvironmentWorkloadSpecForDeployment(context.Context, string, string, string) (state.ProjectEnvironmentWorkloadSpec, error)
	PutManagedObjectStorageSecret(context.Context, state.AppSecret) error
	PutProjectEnvironmentEdgePolicy(context.Context, state.ProjectEnvironmentEdgePolicy) (state.ProjectEnvironmentEdgePolicy, error)
	RequeueExpiredInvocations(context.Context, time.Time, int) (int, error)
	ResolveInvocationPinScope(context.Context, string, string, string) (string, error)
	RollbackProjectEnvironmentClone(context.Context, string, string, string) error
}
