package state

import (
	"context"
	"time"
)

// contractTestStore is the store the MemStore/PgStore contract tests in this package run
// against: Store plus every optional store interface and method they use. Contract helpers
// name it through an alias (type fooTestStore = contractTestStore) instead of declaring their own
// Store-sized interface. The compiler generates a wrapper for each method of each interface type
// a package declares and converts to, so the 60 Store-sized interfaces this replaced made this
// test compile peak at 5.1 GB of heap instead of 2.9 GB (ADR-982).
type contractTestStore interface {
	Store // teststorecheck:allow the one Store-sized interface the contracts alias
	AccountAbuseHoldStore
	AdoptionReportInstanceStore
	ApplicationStandardAssignmentInventoryStore
	ApplicationStandardAutomaticMaterializationStore
	ApplicationStandardAutomaticObservationStore
	ApplicationStandardConsumerRosterStore
	ApplicationStandardEgressStore
	ApplicationStandardEnrollmentStore
	ApplicationStandardExceptionStore
	ApplicationStandardImmediateMaterializationStore
	ApplicationStandardLocalIntentStore
	ApplicationStandardLogConsumerClosureStore
	ApplicationStandardLogDeliveryStore
	ApplicationStandardLogHealthStore
	ApplicationStandardLogInventoryStore
	ApplicationStandardMaterializationStore
	ApplicationStandardObservationStore
	ApplicationStandardOperationControlStore
	ApplicationStandardOperationStore
	ApplicationStandardResourceStore
	ApplicationStandardReviewStore
	ApplicationStandardRuntimeRefreshStore
	ApplicationStandardSnapshotCaptureStore
	ApplicationStandardStore
	BaseImageProducerStore
	BaseImageScanStore
	BuildExportPublicationHistoryStore
	BuildExportPublicationStore
	ComputeNodeRuntimeIdentityStore
	DataRepairOperationStore
	DeploymentArtifactScanEvidenceStore
	DeploymentArtifactScanStore
	DeploymentImagePreparationStore
	DeploymentRegistryRootfsStore
	DeploymentRegistryVerificationStore
	DeploymentRuntimeArtifactInputStore
	DeploymentRuntimeProducerInputStore
	DeploymentRuntimeProducerPresenceStore
	DeploymentRuntimeScanStore
	InstanceApplicationStandardAdmissionStore
	InstanceApplicationStandardBootStore
	InstanceApplicationStandardConfigPublisher
	InstanceApplicationStandardPromotionStore
	InstanceApplicationStandardRuntimeQualificationStore
	InstanceApplicationStandardRuntimeReceiptStore
	InvocationLeaseStore
	OperationApprovalStore
	OperationStore
	OperationTimelineStore
	OperatorParkStore
	OrgActivityAppLifecycleMutationStore
	PRPreviewBatchStore
	PRPreviewSetBatchStore
	PRPreviewSetStore
	PlatformTenantStore
	ProjectEnvironmentWorkloadSpecStore
	ProjectReconcileStore
	RegistryImagePreparationStore
	RuntimeConfigReceiptStore
	SourceBuildRootfsStore
	SourceImagePreparationStore

	ListEnabledAppLogDrains(context.Context) ([]AppLogDrain, error)
	RequeueExpiredInvocations(context.Context, time.Time, int) (int, error)
	SetComputeNodeActive(context.Context, string, bool) error
}
