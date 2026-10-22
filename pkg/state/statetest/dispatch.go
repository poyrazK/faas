// Dispatch methods: one per store method a test fake overrides. Each calls the
// override when the fake defines it and the wrapped store otherwise. To let a fake
// override another method, add a method here in the same shape and its name to the
// map; NewMem, NewPg and NewStore panic when a fake overrides a method missing here.

package statetest

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/managedpostgres/checkpointselection"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
	"github.com/onebox-faas/faas/pkg/objectstorage/grantrevocation"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// memDispatched lists the methods Mem forwards to its override.
var memDispatched = map[string]bool{
	"RestoreApp":                             true,
	"RestoreAppWithActivity":                 true,
	"AppTaskByID":                            true,
	"LiveDeployment":                         true,
	"RuntimeConfigReceiptRequired":           true,
	"ResolveEnvironmentGitOpsServiceBinding": true,

	"ClaimNextQueuedBuild":                                    true,
	"ClaimQueuedBuild":                                        true,
	"ListBuildsAwaitingImage":                                 true,
	"ListDeploymentsForOperator":                              true,
	"ListResumableImagePreparations":                          true,
	"PublishImagePreparationLayer":                            true,
	"RequeueBuildIfClaim":                                     true,
	"ScenarioWebhookDeliveryBlocked":                          true,
	"CreateInstanceWithMode":                                  true,
	"CreateAndClaimJobInstance":                               true,
	"CompleteNodeLifecycle":                                   true,
	"DeliverAuditEvent":                                       true,
	"ExecuteOperatorNode":                                     true,
	"SubmitOperatorRecovery":                                  true,
	"ExecuteOperatorColdBoot":                                 true,
	"BindInstanceSnapshotRecovery":                            true,
	"CompleteOperatorPark":                                    true,
	"CompleteOperatorRestart":                                 true,
	"ProcessNextWorkflowInitialManualCompletion":              true,
	"AcceptEntityOutboxDelivery":                              true,
	"AccountPasswordByAccountID":                              true,
	"DeploymentSidecarRAMs":                                   true,
	"ListBillingWindowInstances":                              true,
	"ListInstancesForApp":                                     true,
	"AccountByID":                                             true,
	"InstanceListByNodeForRecovery":                           true,
	"ActiveComputeNodes":                                      true,
	"ActivePrewarmFloor":                                      true,
	"ActiveProjectReleaseSet":                                 true,
	"AdmitPublishedEventRecipient":                            true,
	"AdmitScheduledWorkflow":                                  true,
	"AppByID":                                                 true,
	"AppBySlug":                                               true,
	"GetProductBillingDocument":                               true,
	"ListProductBillingDocuments":                             true,
	"GetProductBillingHandoff":                                true,
	"HandoffProductBilling":                                   true,
	"StatementHandoffBlocked":                                 true,
	"AppRuntimeConfigChangedAt":                               true,
	"AppRuntimeConfigChangedAtInScope":                        true,
	"ClaimPreviewTeardown":                                    true,
	"CloneProjectEnvironment":                                 true,
	"ComputeNodeByID":                                         true,
	"ComputeNodeUsedMB":                                       true,
	"ComputeNodeUsedMBByNode":                                 true,
	"CountWakeBootStarted24h":                                 true,
	"CreateAppTask":                                           true,
	"DeploymentClearSnapshotBackoff":                          true,
	"EnvironmentWorkloadAdmissionRetired":                     true,
	"CreateCheckedRollback":                                   true,
	"CreateInstance":                                          true,
	"CreateProjectEnvironmentPromotion":                       true,
	"CreateTCPListener":                                       true,
	"DeadLetterEventByID":                                     true,
	"DeploymentByID":                                          true,
	"DeploymentSnapshotBackoffActive":                         true,
	"EdgeRuleHitStatsForApp":                                  true,
	"EnqueueInvocation":                                       true,
	"FailRunningInstanceIfOwnedByNode":                        true,
	"FinalizeManagedPostgresBindingRotationsForApp":           true,
	"FinancialEvidenceCoverage":                               true,
	"FinalizeObjectS3CredentialRotationsForApp":               true,
	"GetAppErrorSample":                                       true,
	"GetFeatureFlags":                                         true,
	"GetObjectBucketObjectLock":                               true,
	"GetRouteHealthReport":                                    true,
	"HasSnapshotHistory":                                      true,
	"InstanceByID":                                            true,
	"IssueObjectMultipartURLCredential":                       true,
	"JobRunAttachSession":                                     true,
	"LatestParkedDeploymentForApp":                            true,
	"ListAppErrorGroups":                                      true,
	"ListAppErrorRequests":                                    true,
	"ListBindingRefreshInventory":                             true,
	"ListBindingVerificationTasks":                            true,
	"ListEnvironmentQualificationExecutionsForRecovery":       true,
	"ListEnvironmentWorkloadQualificationsForDispatch":        true,
	"ListInstancesForAccount":                                 true,
	"ListObjectStorageBindingsForApp":                         true,
	"ListOutboundAppBindings":                                 true,
	"ListQueueBindingConsumersForApp":                         true,
	"ListQueueBindingsForApp":                                 true,
	"ListRunningInstancesOnDeadNodes":                         true,
	"ListTCPListenerTLSObservations":                          true,
	"ListWorkflowScheduleCandidates":                          true,
	"LiveDeploymentForScope":                                  true,
	"FinancialComputeBalance":                                 true,
	"FinancialComputePolicies":                                true,
	"ManagedPostgresAdmissionFenced":                          true,
	"MigrateInstanceOwner":                                    true,
	"MigrateInstanceOwnerWithRuntimeConfig":                   true,
	"NodeList":                                                true,
	"ObserveNextWorkflowFailureEligibility":                   true,
	"ProcessNextEventRecoveryExecutionNotification":           true,
	"ProcessNextWorkflowRecoveryAutomaticBlockedNotification": true,
	"ProcessNextWorkflowRecoveryAutomaticPolicy":              true,
	"ProcessNextWorkflowRecoveryExecutionAttention":           true,
	"ProcessNextWorkflowRecoveryHealthAlert":                  true,
	"ProcessNextWorkflowRecoveryPolicy":                       true,
	"ProcessNextWorkflowRecoveryReviewCompletion":             true,
	"ProcessNextWorkflowRecoveryReviewNotification":           true,
	"PromoteDeploymentWithBindings":                           true,
	"PruneWorkflowRecoveryHistory":                            true,
	"PublishOwnedInstanceRuntime":                             true,
	"QueueBindingByID":                                        true,
	"QueueStateForBinding":                                    true,
	"ReadBindingApplicationAdoption":                          true,
	"ReadBindingPromotionRevision":                            true,
	"ReadBindingRuntimeInventory":                             true,
	"ReadServiceBindingRevision":                              true,
	"RecordEnvironmentQualificationSnapshot":                  true,
	"RecordPublishedEventRecipientProgress":                   true,
	"RecordUploadCommitOutcome":                               true,
	"RecoverCanaryRouteHealth":                                true,
	"RenewInvocationLease":                                    true,
	"RenewOperationExecution":                                 true,
	"RequestTelemetryAnalyticsByDimension":                    true,
	"RequestTelemetryAnalyticsByRoute":                        true,
	"RequestTelemetryRouteCustomers":                          true,
	"ResolveEnvironmentQualificationService":                  true,
	"ResolveInstanceMigrationCommit":                          true,
	"ResolveProjectServiceBinding":                            true,
	"RouteAdviceConsumers":                                    true,
	"RouteAdviceRouteStats":                                   true,
	"RouteAdviceThrottleExcess":                               true,
	"ListRouteInsights":                                       true,
	"GetRouteInsight":                                         true,
	"MarkRouteInsightsFixed":                                  true,
	"ResolveStaleRouteInsights":                               true,
	"RouteAwakeMinutes":                                       true,
	"RouteInsightCandidateApps":                               true,
	"RouteInsightsDetectedSince":                              true,
	"UpsertRouteInsight":                                      true,
	"RouteAwakeRouteMinutes":                                  true,
	"RouteAwakeSourceMinutes":                                 true,
	"RuntimeAppValuesForDeployment":                           true,
	"RuntimeScalingStateForDeployment":                        true,
	"SafeReleaseWorkerLeaseHealth":                            true,
	"SetAccountPassword":                                      true,
	"SetInstanceStartupCPUBoostUntil":                         true,
	"StampOperationExecutionAttempt":                          true,
	"UpdateAccountProviderCustomerID":                         true,
	"WarmPoolReconciliationAppIDs":                            true,
	"AppTaskAttachByTask":                                     true,
	"ColdWakeRatePctSince":                                    true,
	"CountNewErrorFingerprintsSince":                          true,
	"DailyCostCents":                                          true,
	"DeleteSnapshotsByID":                                     true,
	"DeploymentRuntimeUpgradeTarget":                          true,
	"FailDeploymentWithHostingReceipt":                        true,
	"GetApplicationStandardEnrollment":                        true,
	"GetApplicationStandardSnapshotCapture":                   true,
	"GetFreshBaseImageScan":                                   true,
	"GetFreshBuildExportPublication":                          true,
	"GetFreshDeploymentRuntimeProducerInputs":                 true,
	"GetFreshDeploymentRuntimeScan":                           true,
	"GetInstanceApplicationStandardAdmission":                 true,
	"GetInstanceApplicationStandardRuntimeReceipt":            true,
	"IssueApplicationStandardSnapshotCapture":                 true,
	"JobClaimLegacyArtifactVerification":                      true,
	"JobClaimPendingImageMaterialization":                     true,
	"JobFinishLegacyArtifactVerification":                     true,
	"JobPublishImageMaterialization":                          true,
	"JobRenewImageMaterializationLease":                       true,
	"JobRetryLegacyArtifactVerification":                      true,
	"LatestAppEgressPolicyRevision":                           true,
	"LatestSnapshotForTier":                                   true,
	"ListAppDeploymentAuditBetween":                           true,
	"ListMCPReadinessMonitoring":                              true,
	"ListPendingApplicationStandardEgress":                    true,
	"ListSnapshotsForGC":                                      true,
	"MCPConnectionHealth":                                     true,
	"MarkOldSnapshotsStale":                                   true,
	"PublishApplicationStandardSnapshotCapture":               true,
	"PublishBaseImageProducer":                                true,
	"PublishBaseImageScan":                                    true,
	"PublishDeploymentRuntimeScan":                            true,
	"PublishSnapshotIfRuntimeFresh":                           true,
	"RecordApplicationStandardEgress":                         true,
	"RecordDeploymentRegistryVerification":                    true,
	"RuntimeReleaseQualification":                             true,
	"SetDeploymentFailed":                                     true,
	"UpdateDeploymentHostingVerification":                     true,
	"UpsertDeploymentHostingReceipt":                          true,
	"ValidateDeploymentRuntimeUpgradeBaseline":                true,
	"WorkflowAlertSnapshot":                                   true,
	"WorkflowRecoveryReviewCompletionNotificationAllowed":     true,
	"WorkflowRecoveryReviewNotificationAllowed":               true,

	"PutIdempotent":                                 true,
	"AttachDeploymentSubmission":                    true,
	"CreateBuildWithID":                             true,
	"UpdateProjectEnvironmentPromotionWorkload":     true,
	"UpdateProjectEnvironmentPromotionVerification": true,
	"ListPendingSafeProjectSubmissions":             true,
	"ProjectSubmission":                             true,
	"ProjectReleaseSetByID":                         true,
	"AdvanceProjectReleaseVerification":             true,
	"ReconcileSafeProjectSubmission":                true,
	"ReleaseAppTaskByDeployment":                    true,
	"RecordProjectMigrationBaseline":                true,
	"PinnedProjectReleaseWorkers":                   true,
	"HeldProjectReleaseWorkers":                     true,
}

func (s *Mem) AppTaskByID(ctx context.Context, account, app, id string) (state.AppTask, error) {
	if over, ok := s.over.(interface {
		AppTaskByID(context.Context, string, string, string) (state.AppTask, error)
	}); ok {
		return over.AppTaskByID(ctx, account, app, id)
	}
	return s.MemStore.AppTaskByID(ctx, account, app, id)
}

func (s *Mem) RuntimeConfigReceiptRequired(ctx context.Context, appID, scope string) (bool, error) {
	if o, ok := s.over.(interface {
		RuntimeConfigReceiptRequired(context.Context, string, string) (bool, error)
	}); ok {
		return o.RuntimeConfigReceiptRequired(ctx, appID, scope)
	}
	return s.MemStore.RuntimeConfigReceiptRequired(ctx, appID, scope)
}

func (s *Mem) LiveDeployment(ctx context.Context, appID string) (state.Deployment, error) {
	if o, ok := s.over.(interface {
		LiveDeployment(context.Context, string) (state.Deployment, error)
	}); ok {
		return o.LiveDeployment(ctx, appID)
	}
	return s.MemStore.LiveDeployment(ctx, appID)
}

func (s *Mem) ResolveEnvironmentGitOpsServiceBinding(ctx context.Context, appID, deploymentID, service string) (state.EnvironmentGitOpsServiceBindingRoute, bool, bool, error) {
	if o, ok := s.over.(state.EnvironmentGitOpsServiceBindingStore); ok {
		return o.ResolveEnvironmentGitOpsServiceBinding(ctx, appID, deploymentID, service)
	}
	return s.MemStore.ResolveEnvironmentGitOpsServiceBinding(ctx, appID, deploymentID, service)
}

func (s *Mem) CompleteOperatorRestart(ctx context.Context, completion state.OperatorRestartCompletion) (state.OperatorRestartResult, error) {
	if override, ok := s.over.(interface {
		CompleteOperatorRestart(context.Context, state.OperatorRestartCompletion) (state.OperatorRestartResult, error)
	}); ok {
		return override.CompleteOperatorRestart(ctx, completion)
	}
	return s.MemStore.CompleteOperatorRestart(ctx, completion)
}

func (s *Mem) RestoreApp(ctx context.Context, id string, limits api.Limits) (state.App, error) {
	if o, ok := s.over.(interface {
		RestoreApp(context.Context, string, api.Limits) (state.App, error)
	}); ok {
		return o.RestoreApp(ctx, id, limits)
	}
	return s.MemStore.RestoreApp(ctx, id, limits)
}

func (s *Mem) RestoreAppWithActivity(ctx context.Context, id string, limits api.Limits, entry state.OrgActivity) (state.App, int64, error) {
	if o, ok := s.over.(interface {
		RestoreAppWithActivity(context.Context, string, api.Limits, state.OrgActivity) (state.App, int64, error)
	}); ok {
		return o.RestoreAppWithActivity(ctx, id, limits, entry)
	}
	return s.MemStore.RestoreAppWithActivity(ctx, id, limits, entry)
}

func (s *Mem) AcceptEntityOutboxDelivery(p0 context.Context, p1 state.AppWebhookDelivery) (string, error) {
	if o, ok := s.over.(interface {
		AcceptEntityOutboxDelivery(context.Context, state.AppWebhookDelivery) (string, error)
	}); ok {
		return o.AcceptEntityOutboxDelivery(p0, p1)
	}
	return s.MemStore.AcceptEntityOutboxDelivery(p0, p1)
}

func (s *Mem) AccountPasswordByAccountID(p0 context.Context, p1 string) (string, error) {
	if o, ok := s.over.(interface {
		AccountPasswordByAccountID(context.Context, string) (string, error)
	}); ok {
		return o.AccountPasswordByAccountID(p0, p1)
	}
	return s.MemStore.AccountPasswordByAccountID(p0, p1)
}

func (s *Mem) ActiveComputeNodes(p0 context.Context) ([]state.ComputeNode, error) {
	if o, ok := s.over.(interface {
		ActiveComputeNodes(context.Context) ([]state.ComputeNode, error)
	}); ok {
		return o.ActiveComputeNodes(p0)
	}
	return s.MemStore.ActiveComputeNodes(p0)
}

func (s *Mem) ActivePrewarmFloor(p0 context.Context, p1 string, p2 time.Time) (int, error) {
	if o, ok := s.over.(interface {
		ActivePrewarmFloor(context.Context, string, time.Time) (int, error)
	}); ok {
		return o.ActivePrewarmFloor(p0, p1, p2)
	}
	return s.MemStore.ActivePrewarmFloor(p0, p1, p2)
}

func (s *Mem) ActiveProjectReleaseSet(p0 context.Context, p1 string, p2 string, p3 string) (state.ProjectReleaseSet, error) {
	if o, ok := s.over.(interface {
		ActiveProjectReleaseSet(context.Context, string, string, string) (state.ProjectReleaseSet, error)
	}); ok {
		return o.ActiveProjectReleaseSet(p0, p1, p2, p3)
	}
	return s.MemStore.ActiveProjectReleaseSet(p0, p1, p2, p3)
}

func (s *Mem) AdmitPublishedEventRecipient(p0 context.Context, p1 state.PublishedEventRoutingClaim) (state.PublishedEventRoutingResult, error) {
	if o, ok := s.over.(interface {
		AdmitPublishedEventRecipient(context.Context, state.PublishedEventRoutingClaim) (state.PublishedEventRoutingResult, error)
	}); ok {
		return o.AdmitPublishedEventRecipient(p0, p1)
	}
	return s.MemStore.AdmitPublishedEventRecipient(p0, p1)
}

func (s *Mem) AdmitScheduledWorkflow(p0 context.Context, p1 string, p2 string, p3 string, p4 time.Time) (state.WorkflowScheduleCursor, bool, error) {
	if o, ok := s.over.(interface {
		AdmitScheduledWorkflow(context.Context, string, string, string, time.Time) (state.WorkflowScheduleCursor, bool, error)
	}); ok {
		return o.AdmitScheduledWorkflow(p0, p1, p2, p3, p4)
	}
	return s.MemStore.AdmitScheduledWorkflow(p0, p1, p2, p3, p4)
}

func (s *Mem) ComputeNodeByID(p0 context.Context, p1 string) (state.ComputeNode, error) {
	if o, ok := s.over.(interface {
		ComputeNodeByID(context.Context, string) (state.ComputeNode, error)
	}); ok {
		return o.ComputeNodeByID(p0, p1)
	}
	return s.MemStore.ComputeNodeByID(p0, p1)
}

func (s *Mem) DeploymentClearSnapshotBackoff(p0 context.Context, p1 string) error {
	if o, ok := s.over.(interface {
		DeploymentClearSnapshotBackoff(context.Context, string) error
	}); ok {
		return o.DeploymentClearSnapshotBackoff(p0, p1)
	}
	return s.MemStore.DeploymentClearSnapshotBackoff(p0, p1)
}

func (s *Mem) FinancialEvidenceCoverage(p0 context.Context) (time.Time, error) {
	if o, ok := s.over.(interface {
		FinancialEvidenceCoverage(context.Context) (time.Time, error)
	}); ok {
		return o.FinancialEvidenceCoverage(p0)
	}
	return s.MemStore.FinancialEvidenceCoverage(p0)
}

func (s *Mem) AppByID(p0 context.Context, p1 string) (state.App, error) {
	if o, ok := s.over.(interface {
		AppByID(context.Context, string) (state.App, error)
	}); ok {
		return o.AppByID(p0, p1)
	}
	return s.MemStore.AppByID(p0, p1)
}

func (s *Mem) AppBySlug(p0 context.Context, p1 string) (state.App, error) {
	if o, ok := s.over.(interface {
		AppBySlug(context.Context, string) (state.App, error)
	}); ok {
		return o.AppBySlug(p0, p1)
	}
	return s.MemStore.AppBySlug(p0, p1)
}

func (s *Mem) AppRuntimeConfigChangedAt(p0 context.Context, p1 string) (time.Time, bool, error) {
	if o, ok := s.over.(interface {
		AppRuntimeConfigChangedAt(context.Context, string) (time.Time, bool, error)
	}); ok {
		return o.AppRuntimeConfigChangedAt(p0, p1)
	}
	return s.MemStore.AppRuntimeConfigChangedAt(p0, p1)
}

func (s *Mem) AppRuntimeConfigChangedAtInScope(p0 context.Context, p1 string, p2 string) (time.Time, bool, error) {
	if o, ok := s.over.(interface {
		AppRuntimeConfigChangedAtInScope(context.Context, string, string) (time.Time, bool, error)
	}); ok {
		return o.AppRuntimeConfigChangedAtInScope(p0, p1, p2)
	}
	return s.MemStore.AppRuntimeConfigChangedAtInScope(p0, p1, p2)
}

func (s *Mem) ClaimPreviewTeardown(p0 context.Context, p1 state.App, p2 time.Time) (state.App, error) {
	if o, ok := s.over.(interface {
		ClaimPreviewTeardown(context.Context, state.App, time.Time) (state.App, error)
	}); ok {
		return o.ClaimPreviewTeardown(p0, p1, p2)
	}
	return s.MemStore.ClaimPreviewTeardown(p0, p1, p2)
}

func (s *Mem) CloneProjectEnvironment(p0 context.Context, p1 state.ProjectEnvironmentClone, p2 api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error) {
	if o, ok := s.over.(interface {
		CloneProjectEnvironment(context.Context, state.ProjectEnvironmentClone, api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error)
	}); ok {
		return o.CloneProjectEnvironment(p0, p1, p2)
	}
	return s.MemStore.CloneProjectEnvironment(p0, p1, p2)
}

func (s *Mem) ComputeNodeUsedMB(p0 context.Context, p1 string) (int64, error) {
	if o, ok := s.over.(interface {
		ComputeNodeUsedMB(context.Context, string) (int64, error)
	}); ok {
		return o.ComputeNodeUsedMB(p0, p1)
	}
	return s.MemStore.ComputeNodeUsedMB(p0, p1)
}

func (s *Mem) ComputeNodeUsedMBByNode(p0 context.Context, p1 []string) (map[string]int64, error) {
	if o, ok := s.over.(interface {
		ComputeNodeUsedMBByNode(context.Context, []string) (map[string]int64, error)
	}); ok {
		return o.ComputeNodeUsedMBByNode(p0, p1)
	}
	return s.MemStore.ComputeNodeUsedMBByNode(p0, p1)
}

func (s *Mem) CountWakeBootStarted24h(p0 context.Context, p1 string) (int64, error) {
	if o, ok := s.over.(interface {
		CountWakeBootStarted24h(context.Context, string) (int64, error)
	}); ok {
		return o.CountWakeBootStarted24h(p0, p1)
	}
	return s.MemStore.CountWakeBootStarted24h(p0, p1)
}

func (s *Mem) CreateAppTask(p0 context.Context, p1 state.CreateAppTaskParams) (state.AppTask, error) {
	if o, ok := s.over.(interface {
		CreateAppTask(context.Context, state.CreateAppTaskParams) (state.AppTask, error)
	}); ok {
		return o.CreateAppTask(p0, p1)
	}
	return s.MemStore.CreateAppTask(p0, p1)
}

func (s *Mem) CreateCheckedRollback(p0 context.Context, p1 string, p2 string, p3 string, p4 string, p5 string) (api.RollbackOperation, error) {
	if o, ok := s.over.(interface {
		CreateCheckedRollback(context.Context, string, string, string, string, string) (api.RollbackOperation, error)
	}); ok {
		return o.CreateCheckedRollback(p0, p1, p2, p3, p4, p5)
	}
	return s.MemStore.CreateCheckedRollback(p0, p1, p2, p3, p4, p5)
}

func (s *Mem) CreateInstance(p0 context.Context, p1 string, p2 string, p3 string, p4 int, p5 string, p6 string) (state.Instance, error) {
	if o, ok := s.over.(interface {
		CreateInstance(context.Context, string, string, string, int, string, string) (state.Instance, error)
	}); ok {
		return o.CreateInstance(p0, p1, p2, p3, p4, p5, p6)
	}
	return s.MemStore.CreateInstance(p0, p1, p2, p3, p4, p5, p6)
}

func (s *Mem) CreateProjectEnvironmentPromotion(p0 context.Context, p1 state.ProjectEnvironmentPromotion, p2 []state.ProjectEnvironmentPromotionWorkload) (state.ProjectEnvironmentPromotion, []state.ProjectEnvironmentPromotionWorkload, error) {
	if o, ok := s.over.(interface {
		CreateProjectEnvironmentPromotion(context.Context, state.ProjectEnvironmentPromotion, []state.ProjectEnvironmentPromotionWorkload) (state.ProjectEnvironmentPromotion, []state.ProjectEnvironmentPromotionWorkload, error)
	}); ok {
		return o.CreateProjectEnvironmentPromotion(p0, p1, p2)
	}
	return s.MemStore.CreateProjectEnvironmentPromotion(p0, p1, p2)
}

func (s *Mem) CreateTCPListener(p0 context.Context, p1 state.TCPListener) (state.TCPListener, error) {
	if o, ok := s.over.(interface {
		CreateTCPListener(context.Context, state.TCPListener) (state.TCPListener, error)
	}); ok {
		return o.CreateTCPListener(p0, p1)
	}
	return s.MemStore.CreateTCPListener(p0, p1)
}

func (s *Mem) DeadLetterEventByID(p0 context.Context, p1 string, p2 string) (state.DeadLetterEvent, error) {
	if o, ok := s.over.(interface {
		DeadLetterEventByID(context.Context, string, string) (state.DeadLetterEvent, error)
	}); ok {
		return o.DeadLetterEventByID(p0, p1, p2)
	}
	return s.MemStore.DeadLetterEventByID(p0, p1, p2)
}

func (s *Mem) DeploymentByID(p0 context.Context, p1 string) (state.Deployment, error) {
	if o, ok := s.over.(interface {
		DeploymentByID(context.Context, string) (state.Deployment, error)
	}); ok {
		return o.DeploymentByID(p0, p1)
	}
	return s.MemStore.DeploymentByID(p0, p1)
}

func (s *Mem) DeploymentSnapshotBackoffActive(p0 context.Context, p1 string) (state.Deployment, bool, error) {
	if o, ok := s.over.(interface {
		DeploymentSnapshotBackoffActive(context.Context, string) (state.Deployment, bool, error)
	}); ok {
		return o.DeploymentSnapshotBackoffActive(p0, p1)
	}
	return s.MemStore.DeploymentSnapshotBackoffActive(p0, p1)
}

func (s *Mem) EdgeRuleHitStatsForApp(p0 context.Context, p1 string, p2 time.Time) ([]state.EdgeRuleHitStats, error) {
	if o, ok := s.over.(interface {
		EdgeRuleHitStatsForApp(context.Context, string, time.Time) ([]state.EdgeRuleHitStats, error)
	}); ok {
		return o.EdgeRuleHitStatsForApp(p0, p1, p2)
	}
	return s.MemStore.EdgeRuleHitStatsForApp(p0, p1, p2)
}

func (s *Mem) RouteInsightsDetectedSince(p0 context.Context, p1 string, p2 time.Time, p3 int) ([]state.RouteInsightDetection, int64, error) {
	if o, ok := s.over.(interface {
		RouteInsightsDetectedSince(context.Context, string, time.Time, int) ([]state.RouteInsightDetection, int64, error)
	}); ok {
		return o.RouteInsightsDetectedSince(p0, p1, p2, p3)
	}
	return s.MemStore.RouteInsightsDetectedSince(p0, p1, p2, p3)
}

func (s *Mem) EnvironmentWorkloadAdmissionRetired(p0 context.Context, p1 string, p2 string) (bool, error) {
	if o, ok := s.over.(interface {
		EnvironmentWorkloadAdmissionRetired(context.Context, string, string) (bool, error)
	}); ok {
		return o.EnvironmentWorkloadAdmissionRetired(p0, p1, p2)
	}
	return s.MemStore.EnvironmentWorkloadAdmissionRetired(p0, p1, p2)
}

func (s *Mem) EnqueueInvocation(p0 context.Context, p1 state.Invocation) (state.Invocation, error) {
	if o, ok := s.over.(interface {
		EnqueueInvocation(context.Context, state.Invocation) (state.Invocation, error)
	}); ok {
		return o.EnqueueInvocation(p0, p1)
	}
	return s.MemStore.EnqueueInvocation(p0, p1)
}

func (s *Mem) FailRunningInstanceIfOwnedByNode(p0 context.Context, p1 string, p2 string, p3 time.Time) error {
	if o, ok := s.over.(interface {
		FailRunningInstanceIfOwnedByNode(context.Context, string, string, time.Time) error
	}); ok {
		return o.FailRunningInstanceIfOwnedByNode(p0, p1, p2, p3)
	}
	return s.MemStore.FailRunningInstanceIfOwnedByNode(p0, p1, p2, p3)
}

func (s *Mem) FinalizeManagedPostgresBindingRotationsForApp(p0 context.Context, p1 string, p2 string) error {
	if o, ok := s.over.(interface {
		FinalizeManagedPostgresBindingRotationsForApp(context.Context, string, string) error
	}); ok {
		return o.FinalizeManagedPostgresBindingRotationsForApp(p0, p1, p2)
	}
	return s.MemStore.FinalizeManagedPostgresBindingRotationsForApp(p0, p1, p2)
}

func (s *Mem) FinalizeObjectS3CredentialRotationsForApp(p0 context.Context, p1 string, p2 string) error {
	if o, ok := s.over.(interface {
		FinalizeObjectS3CredentialRotationsForApp(context.Context, string, string) error
	}); ok {
		return o.FinalizeObjectS3CredentialRotationsForApp(p0, p1, p2)
	}
	return s.MemStore.FinalizeObjectS3CredentialRotationsForApp(p0, p1, p2)
}

func (s *Mem) GetAppErrorSample(p0 context.Context, p1 sqlc.GetAppErrorSampleParams) (state.AppErrorSampleRow, error) {
	if o, ok := s.over.(interface {
		GetAppErrorSample(context.Context, sqlc.GetAppErrorSampleParams) (state.AppErrorSampleRow, error)
	}); ok {
		return o.GetAppErrorSample(p0, p1)
	}
	return s.MemStore.GetAppErrorSample(p0, p1)
}

func (s *Mem) GetFeatureFlags(p0 context.Context, p1 state.FeatureFlagScope, p2 int64) (state.FeatureFlagVersion, error) {
	if o, ok := s.over.(interface {
		GetFeatureFlags(context.Context, state.FeatureFlagScope, int64) (state.FeatureFlagVersion, error)
	}); ok {
		return o.GetFeatureFlags(p0, p1, p2)
	}
	return s.MemStore.GetFeatureFlags(p0, p1, p2)
}

func (s *Mem) GetObjectBucketObjectLock(p0 context.Context, p1 string, p2 string, p3 string) (state.ObjectBucketObjectLock, error) {
	if o, ok := s.over.(interface {
		GetObjectBucketObjectLock(context.Context, string, string, string) (state.ObjectBucketObjectLock, error)
	}); ok {
		return o.GetObjectBucketObjectLock(p0, p1, p2, p3)
	}
	return s.MemStore.GetObjectBucketObjectLock(p0, p1, p2, p3)
}

func (s *Mem) GetRouteHealthReport(p0 context.Context, p1 string, p2 string, p3 string) (api.RouteHealthReport, error) {
	if o, ok := s.over.(interface {
		GetRouteHealthReport(context.Context, string, string, string) (api.RouteHealthReport, error)
	}); ok {
		return o.GetRouteHealthReport(p0, p1, p2, p3)
	}
	return s.MemStore.GetRouteHealthReport(p0, p1, p2, p3)
}

func (s *Mem) HasSnapshotHistory(p0 context.Context, p1 string) (bool, error) {
	if o, ok := s.over.(interface {
		HasSnapshotHistory(context.Context, string) (bool, error)
	}); ok {
		return o.HasSnapshotHistory(p0, p1)
	}
	return s.MemStore.HasSnapshotHistory(p0, p1)
}

func (s *Mem) InstanceByID(p0 context.Context, p1 string) (state.Instance, error) {
	if o, ok := s.over.(interface {
		InstanceByID(context.Context, string) (state.Instance, error)
	}); ok {
		return o.InstanceByID(p0, p1)
	}
	return s.MemStore.InstanceByID(p0, p1)
}

func (s *Mem) IssueObjectMultipartURLCredential(p0 context.Context, p1 state.ObjectS3Credential, p2 state.ObjectMultipartUpload, p3 api.ObjectStoragePolicy) (state.ObjectS3Credential, error) {
	if o, ok := s.over.(interface {
		IssueObjectMultipartURLCredential(context.Context, state.ObjectS3Credential, state.ObjectMultipartUpload, api.ObjectStoragePolicy) (state.ObjectS3Credential, error)
	}); ok {
		return o.IssueObjectMultipartURLCredential(p0, p1, p2, p3)
	}
	return s.MemStore.IssueObjectMultipartURLCredential(p0, p1, p2, p3)
}

func (s *Mem) JobRunAttachSession(p0 context.Context, p1 string) (state.JobRunAttach, error) {
	if o, ok := s.over.(interface {
		JobRunAttachSession(context.Context, string) (state.JobRunAttach, error)
	}); ok {
		return o.JobRunAttachSession(p0, p1)
	}
	return s.MemStore.JobRunAttachSession(p0, p1)
}

func (s *Mem) LatestParkedDeploymentForApp(p0 context.Context, p1 string) (state.Deployment, error) {
	if o, ok := s.over.(interface {
		LatestParkedDeploymentForApp(context.Context, string) (state.Deployment, error)
	}); ok {
		return o.LatestParkedDeploymentForApp(p0, p1)
	}
	return s.MemStore.LatestParkedDeploymentForApp(p0, p1)
}

func (s *Mem) ListAppErrorGroups(p0 context.Context, p1 sqlc.ListAppErrorGroupsParams) ([]state.AppErrorGroup, error) {
	if o, ok := s.over.(interface {
		ListAppErrorGroups(context.Context, sqlc.ListAppErrorGroupsParams) ([]state.AppErrorGroup, error)
	}); ok {
		return o.ListAppErrorGroups(p0, p1)
	}
	return s.MemStore.ListAppErrorGroups(p0, p1)
}

func (s *Mem) ListAppErrorRequests(p0 context.Context, p1 sqlc.ListAppErrorRequestsParams) ([]state.AppErrorRequestRow, error) {
	if o, ok := s.over.(interface {
		ListAppErrorRequests(context.Context, sqlc.ListAppErrorRequestsParams) ([]state.AppErrorRequestRow, error)
	}); ok {
		return o.ListAppErrorRequests(p0, p1)
	}
	return s.MemStore.ListAppErrorRequests(p0, p1)
}

func (s *Mem) ListBindingRefreshInventory(p0 context.Context, p1 string, p2 string, p3 []string) ([]state.BindingRefreshInventory, error) {
	if o, ok := s.over.(interface {
		ListBindingRefreshInventory(context.Context, string, string, []string) ([]state.BindingRefreshInventory, error)
	}); ok {
		return o.ListBindingRefreshInventory(p0, p1, p2, p3)
	}
	return s.MemStore.ListBindingRefreshInventory(p0, p1, p2, p3)
}

func (s *Mem) ListBindingVerificationTasks(p0 context.Context, p1 string, p2 string, p3 []string, p4 ...state.BindingVerificationSelection) ([]state.BindingVerificationTask, error) {
	if o, ok := s.over.(interface {
		ListBindingVerificationTasks(context.Context, string, string, []string, ...state.BindingVerificationSelection) ([]state.BindingVerificationTask, error)
	}); ok {
		return o.ListBindingVerificationTasks(p0, p1, p2, p3, p4...)
	}
	return s.MemStore.ListBindingVerificationTasks(p0, p1, p2, p3, p4...)
}

func (s *Mem) ListEnvironmentQualificationExecutionsForRecovery(p0 context.Context, p1 string, p2 string, p3 int) ([]state.EnvironmentQualificationExecutionStatus, error) {
	if o, ok := s.over.(interface {
		ListEnvironmentQualificationExecutionsForRecovery(context.Context, string, string, int) ([]state.EnvironmentQualificationExecutionStatus, error)
	}); ok {
		return o.ListEnvironmentQualificationExecutionsForRecovery(p0, p1, p2, p3)
	}
	return s.MemStore.ListEnvironmentQualificationExecutionsForRecovery(p0, p1, p2, p3)
}

func (s *Mem) ListEnvironmentWorkloadQualificationsForDispatch(p0 context.Context, p1 string, p2 string, p3 int) ([]string, error) {
	if o, ok := s.over.(interface {
		ListEnvironmentWorkloadQualificationsForDispatch(context.Context, string, string, int) ([]string, error)
	}); ok {
		return o.ListEnvironmentWorkloadQualificationsForDispatch(p0, p1, p2, p3)
	}
	return s.MemStore.ListEnvironmentWorkloadQualificationsForDispatch(p0, p1, p2, p3)
}

func (s *Mem) ListInstancesForAccount(p0 context.Context, p1 string) ([]state.Instance, error) {
	if o, ok := s.over.(interface {
		ListInstancesForAccount(context.Context, string) ([]state.Instance, error)
	}); ok {
		return o.ListInstancesForAccount(p0, p1)
	}
	return s.MemStore.ListInstancesForAccount(p0, p1)
}

func (s *Mem) ListObjectStorageBindingsForApp(p0 context.Context, p1 string, p2 string, p3 string) ([]state.ObjectStorageBindingInventory, error) {
	if o, ok := s.over.(interface {
		ListObjectStorageBindingsForApp(context.Context, string, string, string) ([]state.ObjectStorageBindingInventory, error)
	}); ok {
		return o.ListObjectStorageBindingsForApp(p0, p1, p2, p3)
	}
	return s.MemStore.ListObjectStorageBindingsForApp(p0, p1, p2, p3)
}

func (s *Mem) ListOutboundAppBindings(p0 context.Context, p1 string, p2 string) ([]state.OutboundAppBinding, error) {
	if o, ok := s.over.(interface {
		ListOutboundAppBindings(context.Context, string, string) ([]state.OutboundAppBinding, error)
	}); ok {
		return o.ListOutboundAppBindings(p0, p1, p2)
	}
	return s.MemStore.ListOutboundAppBindings(p0, p1, p2)
}

func (s *Mem) ListQueueBindingConsumersForApp(p0 context.Context, p1 string, p2 string) ([]state.QueueBindingConsumerInventory, error) {
	if o, ok := s.over.(interface {
		ListQueueBindingConsumersForApp(context.Context, string, string) ([]state.QueueBindingConsumerInventory, error)
	}); ok {
		return o.ListQueueBindingConsumersForApp(p0, p1, p2)
	}
	return s.MemStore.ListQueueBindingConsumersForApp(p0, p1, p2)
}

func (s *Mem) ListQueueBindingsForApp(p0 context.Context, p1 string, p2 string) ([]state.QueueBinding, error) {
	if o, ok := s.over.(interface {
		ListQueueBindingsForApp(context.Context, string, string) ([]state.QueueBinding, error)
	}); ok {
		return o.ListQueueBindingsForApp(p0, p1, p2)
	}
	return s.MemStore.ListQueueBindingsForApp(p0, p1, p2)
}

func (s *Mem) ListRunningInstancesOnDeadNodes(p0 context.Context, p1 time.Time, p2 int) ([]state.Instance, error) {
	if o, ok := s.over.(interface {
		ListRunningInstancesOnDeadNodes(context.Context, time.Time, int) ([]state.Instance, error)
	}); ok {
		return o.ListRunningInstancesOnDeadNodes(p0, p1, p2)
	}
	return s.MemStore.ListRunningInstancesOnDeadNodes(p0, p1, p2)
}

func (s *Mem) ListTCPListenerTLSObservations(p0 context.Context, p1 string) ([]state.TCPListenerTLSObservation, error) {
	if o, ok := s.over.(interface {
		ListTCPListenerTLSObservations(context.Context, string) ([]state.TCPListenerTLSObservation, error)
	}); ok {
		return o.ListTCPListenerTLSObservations(p0, p1)
	}
	return s.MemStore.ListTCPListenerTLSObservations(p0, p1)
}

func (s *Mem) ListWorkflowScheduleCandidates(p0 context.Context, p1 string, p2 string, p3 int) ([]state.WorkflowScheduleCandidate, error) {
	if o, ok := s.over.(interface {
		ListWorkflowScheduleCandidates(context.Context, string, string, int) ([]state.WorkflowScheduleCandidate, error)
	}); ok {
		return o.ListWorkflowScheduleCandidates(p0, p1, p2, p3)
	}
	return s.MemStore.ListWorkflowScheduleCandidates(p0, p1, p2, p3)
}

func (s *Mem) LiveDeploymentForScope(p0 context.Context, p1 string, p2 string) (state.Deployment, error) {
	if o, ok := s.over.(interface {
		LiveDeploymentForScope(context.Context, string, string) (state.Deployment, error)
	}); ok {
		return o.LiveDeploymentForScope(p0, p1, p2)
	}
	return s.MemStore.LiveDeploymentForScope(p0, p1, p2)
}

func (s *Mem) FinancialComputeBalance(p0 context.Context, p1 state.FinancialBudget, p2 time.Time) (financial.ComputeBalance, error) {
	if o, ok := s.over.(interface {
		FinancialComputeBalance(context.Context, state.FinancialBudget, time.Time) (financial.ComputeBalance, error)
	}); ok {
		return o.FinancialComputeBalance(p0, p1, p2)
	}
	return s.MemStore.FinancialComputeBalance(p0, p1, p2)
}

func (s *Mem) FinancialComputePolicies(p0 context.Context, p1 string, p2 string) ([]state.FinancialBudget, error) {
	if o, ok := s.over.(interface {
		FinancialComputePolicies(context.Context, string, string) ([]state.FinancialBudget, error)
	}); ok {
		return o.FinancialComputePolicies(p0, p1, p2)
	}
	return s.MemStore.FinancialComputePolicies(p0, p1, p2)
}

func (s *Mem) ManagedPostgresAdmissionFenced(p0 context.Context, p1 string) (bool, error) {
	if o, ok := s.over.(interface {
		ManagedPostgresAdmissionFenced(context.Context, string) (bool, error)
	}); ok {
		return o.ManagedPostgresAdmissionFenced(p0, p1)
	}
	return s.MemStore.ManagedPostgresAdmissionFenced(p0, p1)
}

func (s *Mem) MigrateInstanceOwner(p0 context.Context, p1 string, p2 string, p3 string, p4 string) error {
	if o, ok := s.over.(interface {
		MigrateInstanceOwner(context.Context, string, string, string, string) error
	}); ok {
		return o.MigrateInstanceOwner(p0, p1, p2, p3, p4)
	}
	return s.MemStore.MigrateInstanceOwner(p0, p1, p2, p3, p4)
}

func (s *Mem) MigrateInstanceOwnerWithRuntimeConfig(p0 context.Context, p1 string, p2 string, p3 string, p4 string, p5 state.RuntimeConfigMigration) error {
	if o, ok := s.over.(interface {
		MigrateInstanceOwnerWithRuntimeConfig(context.Context, string, string, string, string, state.RuntimeConfigMigration) error
	}); ok {
		return o.MigrateInstanceOwnerWithRuntimeConfig(p0, p1, p2, p3, p4, p5)
	}
	return s.MemStore.MigrateInstanceOwnerWithRuntimeConfig(p0, p1, p2, p3, p4, p5)
}

func (s *Mem) NodeList(p0 context.Context, p1 state.NodeLifecycle) ([]state.ComputeNode, error) {
	if o, ok := s.over.(interface {
		NodeList(context.Context, state.NodeLifecycle) ([]state.ComputeNode, error)
	}); ok {
		return o.NodeList(p0, p1)
	}
	return s.MemStore.NodeList(p0, p1)
}

func (s *Mem) ObserveNextWorkflowFailureEligibility(p0 context.Context, p1 time.Time) (bool, error) {
	if o, ok := s.over.(interface {
		ObserveNextWorkflowFailureEligibility(context.Context, time.Time) (bool, error)
	}); ok {
		return o.ObserveNextWorkflowFailureEligibility(p0, p1)
	}
	return s.MemStore.ObserveNextWorkflowFailureEligibility(p0, p1)
}

func (s *Mem) ProcessNextEventRecoveryExecutionNotification(p0 context.Context, p1 time.Time) (bool, error) {
	if o, ok := s.over.(interface {
		ProcessNextEventRecoveryExecutionNotification(context.Context, time.Time) (bool, error)
	}); ok {
		return o.ProcessNextEventRecoveryExecutionNotification(p0, p1)
	}
	return s.MemStore.ProcessNextEventRecoveryExecutionNotification(p0, p1)
}

func (s *Mem) ProcessNextWorkflowRecoveryAutomaticBlockedNotification(p0 context.Context, p1 time.Time) (bool, error) {
	if o, ok := s.over.(interface {
		ProcessNextWorkflowRecoveryAutomaticBlockedNotification(context.Context, time.Time) (bool, error)
	}); ok {
		return o.ProcessNextWorkflowRecoveryAutomaticBlockedNotification(p0, p1)
	}
	return s.MemStore.ProcessNextWorkflowRecoveryAutomaticBlockedNotification(p0, p1)
}

func (s *Mem) ProcessNextWorkflowRecoveryAutomaticPolicy(p0 context.Context, p1 time.Time) (bool, error) {
	if o, ok := s.over.(interface {
		ProcessNextWorkflowRecoveryAutomaticPolicy(context.Context, time.Time) (bool, error)
	}); ok {
		return o.ProcessNextWorkflowRecoveryAutomaticPolicy(p0, p1)
	}
	return s.MemStore.ProcessNextWorkflowRecoveryAutomaticPolicy(p0, p1)
}

func (s *Mem) ProcessNextWorkflowInitialManualCompletion(ctx context.Context, now time.Time) (bool, error) {
	if o, ok := s.over.(interface {
		ProcessNextWorkflowInitialManualCompletion(context.Context, time.Time) (bool, error)
	}); ok {
		return o.ProcessNextWorkflowInitialManualCompletion(ctx, now)
	}
	return s.MemStore.ProcessNextWorkflowInitialManualCompletion(ctx, now)
}

func (s *Mem) ProcessNextWorkflowRecoveryExecutionAttention(p0 context.Context, p1 time.Time) (bool, error) {
	if o, ok := s.over.(interface {
		ProcessNextWorkflowRecoveryExecutionAttention(context.Context, time.Time) (bool, error)
	}); ok {
		return o.ProcessNextWorkflowRecoveryExecutionAttention(p0, p1)
	}
	return s.MemStore.ProcessNextWorkflowRecoveryExecutionAttention(p0, p1)
}

func (s *Mem) ProcessNextWorkflowRecoveryHealthAlert(p0 context.Context, p1 time.Time) (bool, error) {
	if o, ok := s.over.(interface {
		ProcessNextWorkflowRecoveryHealthAlert(context.Context, time.Time) (bool, error)
	}); ok {
		return o.ProcessNextWorkflowRecoveryHealthAlert(p0, p1)
	}
	return s.MemStore.ProcessNextWorkflowRecoveryHealthAlert(p0, p1)
}

func (s *Mem) ProcessNextWorkflowRecoveryPolicy(p0 context.Context, p1 time.Time) (bool, error) {
	if o, ok := s.over.(interface {
		ProcessNextWorkflowRecoveryPolicy(context.Context, time.Time) (bool, error)
	}); ok {
		return o.ProcessNextWorkflowRecoveryPolicy(p0, p1)
	}
	return s.MemStore.ProcessNextWorkflowRecoveryPolicy(p0, p1)
}

func (s *Mem) ProcessNextWorkflowRecoveryReviewCompletion(p0 context.Context, p1 time.Time) (bool, error) {
	if o, ok := s.over.(interface {
		ProcessNextWorkflowRecoveryReviewCompletion(context.Context, time.Time) (bool, error)
	}); ok {
		return o.ProcessNextWorkflowRecoveryReviewCompletion(p0, p1)
	}
	return s.MemStore.ProcessNextWorkflowRecoveryReviewCompletion(p0, p1)
}

func (s *Mem) ProcessNextWorkflowRecoveryReviewNotification(p0 context.Context, p1 time.Time) (bool, error) {
	if o, ok := s.over.(interface {
		ProcessNextWorkflowRecoveryReviewNotification(context.Context, time.Time) (bool, error)
	}); ok {
		return o.ProcessNextWorkflowRecoveryReviewNotification(p0, p1)
	}
	return s.MemStore.ProcessNextWorkflowRecoveryReviewNotification(p0, p1)
}

func (s *Mem) PromoteDeploymentWithBindings(p0 context.Context, p1 string, p2 state.BindingPromotionFence, p3 string) (state.BindingPromotionResult, error) {
	if o, ok := s.over.(interface {
		PromoteDeploymentWithBindings(context.Context, string, state.BindingPromotionFence, string) (state.BindingPromotionResult, error)
	}); ok {
		return o.PromoteDeploymentWithBindings(p0, p1, p2, p3)
	}
	return s.MemStore.PromoteDeploymentWithBindings(p0, p1, p2, p3)
}

func (s *Mem) PruneWorkflowRecoveryHistory(p0 context.Context, p1 time.Time) (int64, error) {
	if o, ok := s.over.(interface {
		PruneWorkflowRecoveryHistory(context.Context, time.Time) (int64, error)
	}); ok {
		return o.PruneWorkflowRecoveryHistory(p0, p1)
	}
	return s.MemStore.PruneWorkflowRecoveryHistory(p0, p1)
}

func (s *Mem) PublishOwnedInstanceRuntime(p0 context.Context, p1 state.RuntimeInstancePublication) (state.Instance, error) {
	if o, ok := s.over.(interface {
		PublishOwnedInstanceRuntime(context.Context, state.RuntimeInstancePublication) (state.Instance, error)
	}); ok {
		return o.PublishOwnedInstanceRuntime(p0, p1)
	}
	return s.MemStore.PublishOwnedInstanceRuntime(p0, p1)
}

func (s *Mem) QueueBindingByID(p0 context.Context, p1 string, p2 string, p3 string) (state.QueueBinding, error) {
	if o, ok := s.over.(interface {
		QueueBindingByID(context.Context, string, string, string) (state.QueueBinding, error)
	}); ok {
		return o.QueueBindingByID(p0, p1, p2, p3)
	}
	return s.MemStore.QueueBindingByID(p0, p1, p2, p3)
}

func (s *Mem) QueueStateForBinding(p0 context.Context, p1 string, p2 string) (state.QueueStats, error) {
	if o, ok := s.over.(interface {
		QueueStateForBinding(context.Context, string, string) (state.QueueStats, error)
	}); ok {
		return o.QueueStateForBinding(p0, p1, p2)
	}
	return s.MemStore.QueueStateForBinding(p0, p1, p2)
}

func (s *Mem) ReadBindingApplicationAdoption(p0 context.Context, p1 string, p2 string, p3 []state.BindingAdoptionSelector) ([]state.BindingApplicationAdoptionRow, error) {
	if o, ok := s.over.(interface {
		ReadBindingApplicationAdoption(context.Context, string, string, []state.BindingAdoptionSelector) ([]state.BindingApplicationAdoptionRow, error)
	}); ok {
		return o.ReadBindingApplicationAdoption(p0, p1, p2, p3)
	}
	return s.MemStore.ReadBindingApplicationAdoption(p0, p1, p2, p3)
}

func (s *Mem) ReadBindingPromotionRevision(p0 context.Context, p1 string, p2 string) (string, error) {
	if o, ok := s.over.(interface {
		ReadBindingPromotionRevision(context.Context, string, string) (string, error)
	}); ok {
		return o.ReadBindingPromotionRevision(p0, p1, p2)
	}
	return s.MemStore.ReadBindingPromotionRevision(p0, p1, p2)
}

func (s *Mem) ReadBindingRuntimeInventory(p0 context.Context, p1 string, p2 string, p3 string) (state.BindingRuntimeInventory, error) {
	if o, ok := s.over.(interface {
		ReadBindingRuntimeInventory(context.Context, string, string, string) (state.BindingRuntimeInventory, error)
	}); ok {
		return o.ReadBindingRuntimeInventory(p0, p1, p2, p3)
	}
	return s.MemStore.ReadBindingRuntimeInventory(p0, p1, p2, p3)
}

func (s *Mem) ReadServiceBindingRevision(p0 context.Context, p1 string, p2 string) (string, error) {
	if o, ok := s.over.(interface {
		ReadServiceBindingRevision(context.Context, string, string) (string, error)
	}); ok {
		return o.ReadServiceBindingRevision(p0, p1, p2)
	}
	return s.MemStore.ReadServiceBindingRevision(p0, p1, p2)
}

func (s *Mem) RecordEnvironmentQualificationSnapshot(p0 context.Context, p1 state.EnvironmentWorkloadQualificationRequest, p2 state.EnvironmentQualificationExecution, p3 state.EnvironmentQualificationSnapshot) (state.EnvironmentQualificationSnapshotReceipt, error) {
	if o, ok := s.over.(interface {
		RecordEnvironmentQualificationSnapshot(context.Context, state.EnvironmentWorkloadQualificationRequest, state.EnvironmentQualificationExecution, state.EnvironmentQualificationSnapshot) (state.EnvironmentQualificationSnapshotReceipt, error)
	}); ok {
		return o.RecordEnvironmentQualificationSnapshot(p0, p1, p2, p3)
	}
	return s.MemStore.RecordEnvironmentQualificationSnapshot(p0, p1, p2, p3)
}

func (s *Mem) RecordPublishedEventRecipientProgress(p0 context.Context, p1 int64, p2 string, p3 string, p4 state.PublishedEventRecipientProgress) error {
	if o, ok := s.over.(interface {
		RecordPublishedEventRecipientProgress(context.Context, int64, string, string, state.PublishedEventRecipientProgress) error
	}); ok {
		return o.RecordPublishedEventRecipientProgress(p0, p1, p2, p3, p4)
	}
	return s.MemStore.RecordPublishedEventRecipientProgress(p0, p1, p2, p3, p4)
}

func (s *Mem) RecordUploadCommitOutcome(p0 context.Context, p1 sqlc.RecordUploadCommitOutcomeParams) (sqlc.UploadCommitOutcome, error) {
	if o, ok := s.over.(interface {
		RecordUploadCommitOutcome(context.Context, sqlc.RecordUploadCommitOutcomeParams) (sqlc.UploadCommitOutcome, error)
	}); ok {
		return o.RecordUploadCommitOutcome(p0, p1)
	}
	return s.MemStore.RecordUploadCommitOutcome(p0, p1)
}

func (s *Mem) RecoverCanaryRouteHealth(p0 context.Context, p1 string, p2 string, p3 string, p4 int) (state.RouteHealthRecoveryResult, error) {
	if o, ok := s.over.(interface {
		RecoverCanaryRouteHealth(context.Context, string, string, string, int) (state.RouteHealthRecoveryResult, error)
	}); ok {
		return o.RecoverCanaryRouteHealth(p0, p1, p2, p3, p4)
	}
	return s.MemStore.RecoverCanaryRouteHealth(p0, p1, p2, p3, p4)
}

func (s *Mem) RenewInvocationLease(p0 context.Context, p1 string, p2 int, p3 int) (bool, error) {
	if o, ok := s.over.(interface {
		RenewInvocationLease(context.Context, string, int, int) (bool, error)
	}); ok {
		return o.RenewInvocationLease(p0, p1, p2, p3)
	}
	return s.MemStore.RenewInvocationLease(p0, p1, p2, p3)
}

func (s *Mem) RenewOperationExecution(p0 context.Context, p1 string, p2 int, p3 int) (bool, error) {
	if o, ok := s.over.(interface {
		RenewOperationExecution(context.Context, string, int, int) (bool, error)
	}); ok {
		return o.RenewOperationExecution(p0, p1, p2, p3)
	}
	return s.MemStore.RenewOperationExecution(p0, p1, p2, p3)
}

func (s *Mem) RequestTelemetryAnalyticsByDimension(p0 context.Context, p1 sqlc.RequestTelemetryAnalyticsByDimensionParams) ([]sqlc.RequestTelemetryAnalyticsByDimensionRow, error) {
	if o, ok := s.over.(interface {
		RequestTelemetryAnalyticsByDimension(context.Context, sqlc.RequestTelemetryAnalyticsByDimensionParams) ([]sqlc.RequestTelemetryAnalyticsByDimensionRow, error)
	}); ok {
		return o.RequestTelemetryAnalyticsByDimension(p0, p1)
	}
	return s.MemStore.RequestTelemetryAnalyticsByDimension(p0, p1)
}

func (s *Mem) RequestTelemetryAnalyticsByRoute(p0 context.Context, p1 sqlc.RequestTelemetryAnalyticsByRouteParams) ([]sqlc.RequestTelemetryAnalyticsByRouteRow, error) {
	if o, ok := s.over.(interface {
		RequestTelemetryAnalyticsByRoute(context.Context, sqlc.RequestTelemetryAnalyticsByRouteParams) ([]sqlc.RequestTelemetryAnalyticsByRouteRow, error)
	}); ok {
		return o.RequestTelemetryAnalyticsByRoute(p0, p1)
	}
	return s.MemStore.RequestTelemetryAnalyticsByRoute(p0, p1)
}

func (s *Mem) RequestTelemetryRouteCustomers(p0 context.Context, p1 sqlc.RequestTelemetryRouteCustomersParams) ([]sqlc.RequestTelemetryRouteCustomersRow, error) {
	if o, ok := s.over.(interface {
		RequestTelemetryRouteCustomers(context.Context, sqlc.RequestTelemetryRouteCustomersParams) ([]sqlc.RequestTelemetryRouteCustomersRow, error)
	}); ok {
		return o.RequestTelemetryRouteCustomers(p0, p1)
	}
	return s.MemStore.RequestTelemetryRouteCustomers(p0, p1)
}

func (s *Mem) ResolveProjectServiceBinding(p0 context.Context, p1, p2, p3 string, p4 ...string) (state.EnvironmentGitOpsServiceBindingRoute, bool, bool, error) {
	if o, ok := s.over.(state.ProjectServiceGraphStore); ok {
		return o.ResolveProjectServiceBinding(p0, p1, p2, p3, p4...)
	}
	return s.MemStore.ResolveProjectServiceBinding(p0, p1, p2, p3, p4...)
}

func (s *Mem) ResolveEnvironmentQualificationService(p0 context.Context, p1 state.EnvironmentQualificationServiceRequest) (state.EnvironmentQualificationServiceRoute, error) {
	if o, ok := s.over.(interface {
		ResolveEnvironmentQualificationService(context.Context, state.EnvironmentQualificationServiceRequest) (state.EnvironmentQualificationServiceRoute, error)
	}); ok {
		return o.ResolveEnvironmentQualificationService(p0, p1)
	}
	return s.MemStore.ResolveEnvironmentQualificationService(p0, p1)
}

func (s *Mem) ResolveInstanceMigrationCommit(p0 context.Context, p1 state.MigrationCommitAttempt) (state.MigrationCommitResolution, error) {
	if o, ok := s.over.(interface {
		ResolveInstanceMigrationCommit(context.Context, state.MigrationCommitAttempt) (state.MigrationCommitResolution, error)
	}); ok {
		return o.ResolveInstanceMigrationCommit(p0, p1)
	}
	return s.MemStore.ResolveInstanceMigrationCommit(p0, p1)
}

func (s *Mem) RouteAdviceConsumers(p0 context.Context, p1 sqlc.RouteAdviceConsumersParams) ([]sqlc.RouteAdviceConsumersRow, error) {
	if o, ok := s.over.(interface {
		RouteAdviceConsumers(context.Context, sqlc.RouteAdviceConsumersParams) ([]sqlc.RouteAdviceConsumersRow, error)
	}); ok {
		return o.RouteAdviceConsumers(p0, p1)
	}
	return s.MemStore.RouteAdviceConsumers(p0, p1)
}

func (s *Mem) RouteAdviceRouteStats(p0 context.Context, p1 sqlc.RouteAdviceRouteStatsParams) ([]sqlc.RouteAdviceRouteStatsRow, error) {
	if o, ok := s.over.(interface {
		RouteAdviceRouteStats(context.Context, sqlc.RouteAdviceRouteStatsParams) ([]sqlc.RouteAdviceRouteStatsRow, error)
	}); ok {
		return o.RouteAdviceRouteStats(p0, p1)
	}
	return s.MemStore.RouteAdviceRouteStats(p0, p1)
}

func (s *Mem) RouteAdviceThrottleExcess(p0 context.Context, p1 sqlc.RouteAdviceThrottleExcessParams) (int64, error) {
	if o, ok := s.over.(interface {
		RouteAdviceThrottleExcess(context.Context, sqlc.RouteAdviceThrottleExcessParams) (int64, error)
	}); ok {
		return o.RouteAdviceThrottleExcess(p0, p1)
	}
	return s.MemStore.RouteAdviceThrottleExcess(p0, p1)
}

func (s *Mem) ListRouteInsights(p0 context.Context, p1 sqlc.ListRouteInsightsParams) ([]sqlc.ListRouteInsightsRow, error) {
	if o, ok := s.over.(interface {
		ListRouteInsights(context.Context, sqlc.ListRouteInsightsParams) ([]sqlc.ListRouteInsightsRow, error)
	}); ok {
		return o.ListRouteInsights(p0, p1)
	}
	return s.MemStore.ListRouteInsights(p0, p1)
}

func (s *Mem) GetRouteInsight(p0 context.Context, p1 sqlc.GetRouteInsightParams) (sqlc.GetRouteInsightRow, error) {
	if o, ok := s.over.(interface {
		GetRouteInsight(context.Context, sqlc.GetRouteInsightParams) (sqlc.GetRouteInsightRow, error)
	}); ok {
		return o.GetRouteInsight(p0, p1)
	}
	return s.MemStore.GetRouteInsight(p0, p1)
}

func (s *Mem) MarkRouteInsightsFixed(p0 context.Context, p1 sqlc.MarkRouteInsightsFixedParams) (int64, error) {
	if o, ok := s.over.(interface {
		MarkRouteInsightsFixed(context.Context, sqlc.MarkRouteInsightsFixedParams) (int64, error)
	}); ok {
		return o.MarkRouteInsightsFixed(p0, p1)
	}
	return s.MemStore.MarkRouteInsightsFixed(p0, p1)
}

func (s *Mem) ResolveStaleRouteInsights(p0 context.Context, p1 sqlc.ResolveStaleRouteInsightsParams) (int64, error) {
	if o, ok := s.over.(interface {
		ResolveStaleRouteInsights(context.Context, sqlc.ResolveStaleRouteInsightsParams) (int64, error)
	}); ok {
		return o.ResolveStaleRouteInsights(p0, p1)
	}
	return s.MemStore.ResolveStaleRouteInsights(p0, p1)
}

func (s *Mem) RouteInsightCandidateApps(p0 context.Context, p1 sqlc.RouteInsightCandidateAppsParams) ([]sqlc.RouteInsightCandidateAppsRow, error) {
	if o, ok := s.over.(interface {
		RouteInsightCandidateApps(context.Context, sqlc.RouteInsightCandidateAppsParams) ([]sqlc.RouteInsightCandidateAppsRow, error)
	}); ok {
		return o.RouteInsightCandidateApps(p0, p1)
	}
	return s.MemStore.RouteInsightCandidateApps(p0, p1)
}

func (s *Mem) UpsertRouteInsight(p0 context.Context, p1 sqlc.UpsertRouteInsightParams) error {
	if o, ok := s.over.(interface {
		UpsertRouteInsight(context.Context, sqlc.UpsertRouteInsightParams) error
	}); ok {
		return o.UpsertRouteInsight(p0, p1)
	}
	return s.MemStore.UpsertRouteInsight(p0, p1)
}

func (s *Mem) RouteAwakeMinutes(p0 context.Context, p1 sqlc.RouteAwakeMinutesParams) ([]sqlc.RouteAwakeMinutesRow, error) {
	if o, ok := s.over.(interface {
		RouteAwakeMinutes(context.Context, sqlc.RouteAwakeMinutesParams) ([]sqlc.RouteAwakeMinutesRow, error)
	}); ok {
		return o.RouteAwakeMinutes(p0, p1)
	}
	return s.MemStore.RouteAwakeMinutes(p0, p1)
}

func (s *Mem) RouteAwakeRouteMinutes(p0 context.Context, p1 sqlc.RouteAwakeRouteMinutesParams) ([]sqlc.RouteAwakeRouteMinutesRow, error) {
	if o, ok := s.over.(interface {
		RouteAwakeRouteMinutes(context.Context, sqlc.RouteAwakeRouteMinutesParams) ([]sqlc.RouteAwakeRouteMinutesRow, error)
	}); ok {
		return o.RouteAwakeRouteMinutes(p0, p1)
	}
	return s.MemStore.RouteAwakeRouteMinutes(p0, p1)
}

func (s *Mem) RouteAwakeSourceMinutes(p0 context.Context, p1 sqlc.RouteAwakeSourceMinutesParams) ([]sqlc.RouteAwakeSourceMinutesRow, error) {
	if o, ok := s.over.(interface {
		RouteAwakeSourceMinutes(context.Context, sqlc.RouteAwakeSourceMinutesParams) ([]sqlc.RouteAwakeSourceMinutesRow, error)
	}); ok {
		return o.RouteAwakeSourceMinutes(p0, p1)
	}
	return s.MemStore.RouteAwakeSourceMinutes(p0, p1)
}

func (s *Mem) RuntimeAppValuesForDeployment(p0 context.Context, p1 string, p2 string, p3 string) (state.RuntimeAppValuesSnapshot, error) {
	if o, ok := s.over.(interface {
		RuntimeAppValuesForDeployment(context.Context, string, string, string) (state.RuntimeAppValuesSnapshot, error)
	}); ok {
		return o.RuntimeAppValuesForDeployment(p0, p1, p2, p3)
	}
	return s.MemStore.RuntimeAppValuesForDeployment(p0, p1, p2, p3)
}

func (s *Mem) RuntimeScalingStateForDeployment(p0 context.Context, p1 string, p2 string, p3 string) (state.RuntimeScalingState, error) {
	if o, ok := s.over.(interface {
		RuntimeScalingStateForDeployment(context.Context, string, string, string) (state.RuntimeScalingState, error)
	}); ok {
		return o.RuntimeScalingStateForDeployment(p0, p1, p2, p3)
	}
	return s.MemStore.RuntimeScalingStateForDeployment(p0, p1, p2, p3)
}

func (s *Mem) SafeReleaseWorkerLeaseHealth(p0 context.Context) (state.SafeReleaseWorkerLeaseHealth, error) {
	if o, ok := s.over.(interface {
		SafeReleaseWorkerLeaseHealth(context.Context) (state.SafeReleaseWorkerLeaseHealth, error)
	}); ok {
		return o.SafeReleaseWorkerLeaseHealth(p0)
	}
	return s.MemStore.SafeReleaseWorkerLeaseHealth(p0)
}

func (s *Mem) SetAccountPassword(p0 context.Context, p1 string, p2 string) error {
	if o, ok := s.over.(interface {
		SetAccountPassword(context.Context, string, string) error
	}); ok {
		return o.SetAccountPassword(p0, p1, p2)
	}
	return s.MemStore.SetAccountPassword(p0, p1, p2)
}

func (s *Mem) SetInstanceStartupCPUBoostUntil(p0 context.Context, p1 string, p2 *time.Time) error {
	if o, ok := s.over.(interface {
		SetInstanceStartupCPUBoostUntil(context.Context, string, *time.Time) error
	}); ok {
		return o.SetInstanceStartupCPUBoostUntil(p0, p1, p2)
	}
	return s.MemStore.SetInstanceStartupCPUBoostUntil(p0, p1, p2)
}

func (s *Mem) StampOperationExecutionAttempt(p0 context.Context, p1 string, p2 string, p3 int) error {
	if o, ok := s.over.(interface {
		StampOperationExecutionAttempt(context.Context, string, string, int) error
	}); ok {
		return o.StampOperationExecutionAttempt(p0, p1, p2, p3)
	}
	return s.MemStore.StampOperationExecutionAttempt(p0, p1, p2, p3)
}

func (s *Mem) UpdateAccountProviderCustomerID(p0 context.Context, p1 string, p2 string) error {
	if o, ok := s.over.(interface {
		UpdateAccountProviderCustomerID(context.Context, string, string) error
	}); ok {
		return o.UpdateAccountProviderCustomerID(p0, p1, p2)
	}
	return s.MemStore.UpdateAccountProviderCustomerID(p0, p1, p2)
}

func (s *Mem) WarmPoolReconciliationAppIDs(p0 context.Context, p1 string) ([]string, error) {
	if o, ok := s.over.(interface {
		WarmPoolReconciliationAppIDs(context.Context, string) ([]string, error)
	}); ok {
		return o.WarmPoolReconciliationAppIDs(p0, p1)
	}
	return s.MemStore.WarmPoolReconciliationAppIDs(p0, p1)
}

func (s *Mem) DeploymentSidecarRAMs(p0 context.Context, p1 string) ([]int, error) {
	if o, ok := s.over.(interface {
		DeploymentSidecarRAMs(context.Context, string) ([]int, error)
	}); ok {
		return o.DeploymentSidecarRAMs(p0, p1)
	}
	return s.MemStore.DeploymentSidecarRAMs(p0, p1)
}

func (s *Mem) ListBillingWindowInstances(p0 context.Context, p1, p2 time.Time) ([]state.Instance, error) {
	if o, ok := s.over.(interface {
		ListBillingWindowInstances(context.Context, time.Time, time.Time) ([]state.Instance, error)
	}); ok {
		return o.ListBillingWindowInstances(p0, p1, p2)
	}
	return s.MemStore.ListBillingWindowInstances(p0, p1, p2)
}

func (s *Mem) ListInstancesForApp(p0 context.Context, p1 string) ([]state.Instance, error) {
	if o, ok := s.over.(interface {
		ListInstancesForApp(context.Context, string) ([]state.Instance, error)
	}); ok {
		return o.ListInstancesForApp(p0, p1)
	}
	return s.MemStore.ListInstancesForApp(p0, p1)
}

func (s *Mem) AccountByID(p0 context.Context, p1 string) (state.Account, error) {
	if o, ok := s.over.(interface {
		AccountByID(context.Context, string) (state.Account, error)
	}); ok {
		return o.AccountByID(p0, p1)
	}
	return s.MemStore.AccountByID(p0, p1)
}

func (s *Mem) InstanceListByNodeForRecovery(p0 context.Context, p1 string) ([]state.RecoveryInstance, error) {
	if o, ok := s.over.(interface {
		InstanceListByNodeForRecovery(context.Context, string) ([]state.RecoveryInstance, error)
	}); ok {
		return o.InstanceListByNodeForRecovery(p0, p1)
	}
	return s.MemStore.InstanceListByNodeForRecovery(p0, p1)
}

// pgDispatched lists the methods Pg forwards to its override.
var pgDispatched = map[string]bool{
	"MigrateInstanceOwnerWithRuntimeConfig":                              true,
	"ResolveInstanceMigrationCommit":                                     true,
	"ScenarioWebhookDeliveryBlocked":                                     true,
	"SubmitOperatorRecovery":                                             true,
	"AbandonProjectEnvironmentCloneConfigurationFence":                   true,
	"AbandonProjectEnvironmentCloneObjectWriteFences":                    true,
	"AcquireProjectEnvironmentCloneConfigurationFence":                   true,
	"AcquireProjectEnvironmentCloneObjectWriteFence":                     true,
	"AdoptProjectEnvironmentCloneCheckpoint":                             true,
	"AdoptProjectEnvironmentClonePostgresSnapshotRestore":                true,
	"AdvanceProjectEnvironmentCloneCheckpointIntent":                     true,
	"AdvanceProjectEnvironmentCloneOperation":                            true,
	"AllocateProjectEnvironmentClonePostgresVerificationRead":            true,
	"BeginProjectEnvironmentClonePostgresCopyReaderCleanup":              true,
	"BeginProjectEnvironmentClonePostgresCopyTargetCleanup":              true,
	"BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup":         true,
	"ClaimNextProjectEnvironmentClone":                                   true,
	"ClaimProjectEnvironmentCloneCheckpointCaptureRetirement":            true,
	"ClaimProjectEnvironmentCloneCheckpointObjectCopy":                   true,
	"ClaimProjectEnvironmentCloneCheckpointObjectRetirement":             true,
	"ClaimProjectEnvironmentCloneCheckpointObjectTarget":                 true,
	"ClaimProjectEnvironmentCloneCheckpointObjectVerification":           true,
	"ClaimProjectEnvironmentCloneCheckpointPgActivation":                 true,
	"ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement":       true,
	"ClaimProjectEnvironmentCloneCheckpointPostgresCredential":           true,
	"ClaimProjectEnvironmentCloneCheckpointPostgresCredentialRetirement": true,
	"ClaimProjectEnvironmentCloneCheckpointPostgresIsolation":            true,
	"ClaimProjectEnvironmentCloneCheckpointPostgresRuntime":              true,
	"ClaimProjectEnvironmentCloneCheckpointPostgresTargetCompensation":   true,
	"ClaimProjectEnvironmentCloneCheckpointPostgresVerification":         true,
	"ClaimProjectEnvironmentCloneCheckpointRelease":                      true,
	"ClaimProjectEnvironmentCloneCheckpointSourceCompensation":           true,
	"ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement":           true,
	"ClaimProjectEnvironmentClonePostgresArchiveUpload":                  true,
	"ClaimProjectEnvironmentClonePostgresCopyReaderCleanup":              true,
	"ClaimProjectEnvironmentClonePostgresCopyReaderRequest":              true,
	"ClaimProjectEnvironmentClonePostgresImport":                         true,
	"ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest":         true,
	"ClaimProjectEnvironmentClonePostgresVerification":                   true,
	"ClaimProjectEnvironmentClonePostgresVerificationAttempt":            true,
	"CleanupProjectEnvironmentCloneConfigurationForLease":                true,
	"CleanupProjectEnvironmentCloneDeploymentsForLease":                  true,
	"CleanupProjectEnvironmentCloneEnvironmentForLease":                  true,
	"CleanupProjectEnvironmentCloneWorkloadsForLease":                    true,
	"CompleteInvocationClaim":                                            true,
	"CompleteProjectEnvironmentCloneCheckpointCompensation":              true,
	"ConsumerKeyByAppAndPrefix":                                          true,
	"DispatchProjectEnvironmentCloneObjectGrantRevocation":               true,
	"EnqueueInvocation": true,
	"FinishProjectEnvironmentCloneCheckpointCaptureRetirement":            true,
	"FinishProjectEnvironmentCloneCheckpointObjectCopy":                   true,
	"FinishProjectEnvironmentCloneCheckpointObjectRetirement":             true,
	"FinishProjectEnvironmentCloneCheckpointObjectTarget":                 true,
	"FinishProjectEnvironmentCloneCheckpointObjectVerification":           true,
	"FinishProjectEnvironmentCloneCheckpointPgActivation":                 true,
	"FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement":       true,
	"FinishProjectEnvironmentCloneCheckpointPostgresCredential":           true,
	"FinishProjectEnvironmentCloneCheckpointPostgresCredentialRetirement": true,
	"FinishProjectEnvironmentCloneCheckpointPostgresIsolation":            true,
	"FinishProjectEnvironmentCloneCheckpointPostgresRuntime":              true,
	"FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation":   true,
	"FinishProjectEnvironmentCloneCheckpointPostgresVerification":         true,
	"FinishProjectEnvironmentCloneCheckpointRelease":                      true,
	"FinishProjectEnvironmentCloneCheckpointSourceCompensation":           true,
	"FinishProjectEnvironmentCloneCheckpointWorkloadRetirement":           true,
	"FinishProjectEnvironmentClonePostgresCopyReaderCleanup":              true,
	"FinishProjectEnvironmentClonePostgresCopyTargetCleanup":              true,
	"FinishProjectEnvironmentClonePostgresSnapshotCleanup":                true,
	"FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup":         true,
	"FinishProjectEnvironmentClonePostgresWriteFenceAbandonment":          true,
	"GetAPIConsumerByID":                                                     true,
	"MaterializeProjectEnvironmentCloneForLease":                             true,
	"PrepareProjectEnvironmentCloneCheckpointObjectCredentialValues":         true,
	"PrepareProjectEnvironmentClonePostgresBinding":                          true,
	"ProjectEnvironmentCloneConfigurationFenceForLease":                      true,
	"ProjectEnvironmentCloneObjectWriteFencesForLease":                       true,
	"ProjectEnvironmentClonePostgresContentsForLease":                        true,
	"ProjectEnvironmentClonePostgresDatabasePlanForLease":                    true,
	"ProjectEnvironmentClonePostgresWriteFencesForLease":                     true,
	"PublishProjectEnvironmentCloneCheckpointObjects":                        true,
	"PublishProjectEnvironmentCloneCheckpointPgBindings":                     true,
	"RecordProjectEnvironmentCloneObjectGrantRevocation":                     true,
	"RecordProjectEnvironmentClonePostgresArchive":                           true,
	"RecordProjectEnvironmentClonePostgresCheckpointSelection":               true,
	"RecordProjectEnvironmentClonePostgresContents":                          true,
	"RecordProjectEnvironmentClonePostgresCopyReader":                        true,
	"RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations":      true,
	"RecordProjectEnvironmentClonePostgresCopyTarget":                        true,
	"RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentity":         true,
	"RecordProjectEnvironmentClonePostgresDatabasePlan":                      true,
	"RecordProjectEnvironmentClonePostgresDatabaseSQLPins":                   true,
	"RecordProjectEnvironmentClonePostgresImportExecution":                   true,
	"RecordProjectEnvironmentClonePostgresInventory":                         true,
	"RecordProjectEnvironmentClonePostgresMaintenance":                       true,
	"RecordProjectEnvironmentClonePostgresMembershipPlan":                    true,
	"RecordProjectEnvironmentClonePostgresRolePlan":                          true,
	"RecordProjectEnvironmentClonePostgresSnapshot":                          true,
	"RecordProjectEnvironmentClonePostgresSnapshotRestore":                   true,
	"RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity":    true,
	"RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations": true,
	"RecordProjectEnvironmentClonePostgresTargetSQLPins":                     true,
	"RecordProjectEnvironmentClonePostgresVerificationAttemptClosure":        true,
	"RecordProjectEnvironmentClonePostgresVerificationAttemptMatch":          true,
	"RecordProjectEnvironmentClonePostgresVerificationClosure":               true,
	"RecordProjectEnvironmentClonePostgresVerificationFailure":               true,
	"RecordProjectEnvironmentClonePostgresVerificationMatch":                 true,
	"ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation":        true,
	"ReleaseProjectEnvironmentCloneLease":                                    true,
	"RenewProjectEnvironmentCloneLease":                                      true,
	"ReserveProjectEnvironmentCloneCheckpointCaptureRetirement":              true,
	"ReserveProjectEnvironmentCloneCheckpointObjectRetirement":               true,
	"ReserveProjectEnvironmentCloneCheckpointPgActivation":                   true,
	"ReserveProjectEnvironmentCloneCheckpointPgConnectionValue":              true,
	"ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement":         true,
	"ReserveProjectEnvironmentCloneCheckpointPostgresCredentialRetirement":   true,
	"ReserveProjectEnvironmentCloneCheckpointPostgresTargetCompensation":     true,
	"ReserveProjectEnvironmentCloneCheckpointSourceCompensation":             true,
	"ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement":             true,
	"ReserveProjectEnvironmentCloneDatabase":                                 true,
	"ReserveProjectEnvironmentCloneObjectGrantRevocation":                    true,
	"ReserveProjectEnvironmentClonePostgresArchive":                          true,
	"ReserveProjectEnvironmentClonePostgresBinding":                          true,
	"ReserveProjectEnvironmentClonePostgresContents":                         true,
	"ReserveProjectEnvironmentClonePostgresCopyReader":                       true,
	"ReserveProjectEnvironmentClonePostgresCopyTarget":                       true,
	"ReserveProjectEnvironmentClonePostgresImport":                           true,
	"ReserveProjectEnvironmentClonePostgresMaintenance":                      true,
	"ReserveProjectEnvironmentClonePostgresSnapshot":                         true,
	"ReserveProjectEnvironmentClonePostgresSnapshotRestore":                  true,
	"ReserveProjectEnvironmentClonePostgresVerification":                     true,
	"ReserveProjectEnvironmentClonePostgresVerificationReadBudget":           true,
	"ReserveProjectEnvironmentClonePostgresVerificationRetry":                true,
	"SealProjectEnvironmentCloneCheckpointPgConnectionValue":                 true,
	"SealProjectEnvironmentCloneCheckpointPostgresCredential":                true,
	"TouchConsumerKeyLastUsed":                                               true,
	"WithdrawProjectEnvironmentCloneCheckpointObjects":                       true,
	"WithdrawProjectEnvironmentCloneCheckpointPgBindings":                    true,
}

func (s *Pg) AbandonProjectEnvironmentCloneConfigurationFence(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) error {
	if o, ok := s.over.(interface {
		AbandonProjectEnvironmentCloneConfigurationFence(context.Context, state.ProjectEnvironmentCloneLease) error
	}); ok {
		return o.AbandonProjectEnvironmentCloneConfigurationFence(p0, p1)
	}
	return s.PgStore.AbandonProjectEnvironmentCloneConfigurationFence(p0, p1)
}

func (s *Pg) AbandonProjectEnvironmentCloneObjectWriteFences(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) error {
	if o, ok := s.over.(interface {
		AbandonProjectEnvironmentCloneObjectWriteFences(context.Context, state.ProjectEnvironmentCloneLease) error
	}); ok {
		return o.AbandonProjectEnvironmentCloneObjectWriteFences(p0, p1)
	}
	return s.PgStore.AbandonProjectEnvironmentCloneObjectWriteFences(p0, p1)
}

func (s *Pg) AcquireProjectEnvironmentCloneConfigurationFence(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneConfigurationFence, error) {
	if o, ok := s.over.(interface {
		AcquireProjectEnvironmentCloneConfigurationFence(context.Context, state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneConfigurationFence, error)
	}); ok {
		return o.AcquireProjectEnvironmentCloneConfigurationFence(p0, p1)
	}
	return s.PgStore.AcquireProjectEnvironmentCloneConfigurationFence(p0, p1)
}

func (s *Pg) AcquireProjectEnvironmentCloneObjectWriteFence(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ObjectBucketWriteFence, error) {
	if o, ok := s.over.(interface {
		AcquireProjectEnvironmentCloneObjectWriteFence(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ObjectBucketWriteFence, error)
	}); ok {
		return o.AcquireProjectEnvironmentCloneObjectWriteFence(p0, p1, p2)
	}
	return s.PgStore.AcquireProjectEnvironmentCloneObjectWriteFence(p0, p1, p2)
}

func (s *Pg) AdoptProjectEnvironmentCloneCheckpoint(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentCloneCheckpointAdoption, p3 int) (state.ProjectEnvironmentCloneOperation, error) {
	if o, ok := s.over.(interface {
		AdoptProjectEnvironmentCloneCheckpoint(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentCloneCheckpointAdoption, int) (state.ProjectEnvironmentCloneOperation, error)
	}); ok {
		return o.AdoptProjectEnvironmentCloneCheckpoint(p0, p1, p2, p3)
	}
	return s.PgStore.AdoptProjectEnvironmentCloneCheckpoint(p0, p1, p2, p3)
}

func (s *Pg) AdoptProjectEnvironmentClonePostgresSnapshotRestore(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentCloneDatabaseTarget, bool, error) {
	if o, ok := s.over.(interface {
		AdoptProjectEnvironmentClonePostgresSnapshotRestore(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentCloneDatabaseTarget, bool, error)
	}); ok {
		return o.AdoptProjectEnvironmentClonePostgresSnapshotRestore(p0, p1, p2)
	}
	return s.PgStore.AdoptProjectEnvironmentClonePostgresSnapshotRestore(p0, p1, p2)
}

func (s *Pg) AdvanceProjectEnvironmentCloneCheckpointIntent(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 []state.ProjectEnvironmentCloneResource) (state.ProjectEnvironmentCloneOperation, error) {
	if o, ok := s.over.(interface {
		AdvanceProjectEnvironmentCloneCheckpointIntent(context.Context, state.ProjectEnvironmentCloneLease, []state.ProjectEnvironmentCloneResource) (state.ProjectEnvironmentCloneOperation, error)
	}); ok {
		return o.AdvanceProjectEnvironmentCloneCheckpointIntent(p0, p1, p2)
	}
	return s.PgStore.AdvanceProjectEnvironmentCloneCheckpointIntent(p0, p1, p2)
}

func (s *Pg) AdvanceProjectEnvironmentCloneOperation(p0 context.Context, p1 string, p2 string, p3 string, p4 string, p5 string, p6 int64, p7 []state.ProjectEnvironmentCloneResource, p8 string) (state.ProjectEnvironmentCloneOperation, error) {
	if o, ok := s.over.(interface {
		AdvanceProjectEnvironmentCloneOperation(context.Context, string, string, string, string, string, int64, []state.ProjectEnvironmentCloneResource, string) (state.ProjectEnvironmentCloneOperation, error)
	}); ok {
		return o.AdvanceProjectEnvironmentCloneOperation(p0, p1, p2, p3, p4, p5, p6, p7, p8)
	}
	return s.PgStore.AdvanceProjectEnvironmentCloneOperation(p0, p1, p2, p3, p4, p5, p6, p7, p8)
}

func (s *Pg) AllocateProjectEnvironmentClonePostgresVerificationRead(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentClonePostgresVerificationReadRequest) (state.ProjectEnvironmentClonePostgresVerificationReadBudget, bool, error) {
	if o, ok := s.over.(interface {
		AllocateProjectEnvironmentClonePostgresVerificationRead(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresVerificationReadRequest) (state.ProjectEnvironmentClonePostgresVerificationReadBudget, bool, error)
	}); ok {
		return o.AllocateProjectEnvironmentClonePostgresVerificationRead(p0, p1, p2)
	}
	return s.PgStore.AllocateProjectEnvironmentClonePostgresVerificationRead(p0, p1, p2)
}

func (s *Pg) BeginProjectEnvironmentClonePostgresCopyReaderCleanup(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentClonePostgresCopyReader, error) {
	if o, ok := s.over.(interface {
		BeginProjectEnvironmentClonePostgresCopyReaderCleanup(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentClonePostgresCopyReader, error)
	}); ok {
		return o.BeginProjectEnvironmentClonePostgresCopyReaderCleanup(p0, p1, p2)
	}
	return s.PgStore.BeginProjectEnvironmentClonePostgresCopyReaderCleanup(p0, p1, p2)
}

func (s *Pg) BeginProjectEnvironmentClonePostgresCopyTargetCleanup(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentClonePostgresCopyTarget, error) {
	if o, ok := s.over.(interface {
		BeginProjectEnvironmentClonePostgresCopyTargetCleanup(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentClonePostgresCopyTarget, error)
	}); ok {
		return o.BeginProjectEnvironmentClonePostgresCopyTargetCleanup(p0, p1, p2)
	}
	return s.PgStore.BeginProjectEnvironmentClonePostgresCopyTargetCleanup(p0, p1, p2)
}

func (s *Pg) BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	if o, ok := s.over.(interface {
		BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error)
	}); ok {
		return o.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(p0, p1, p2)
	}
	return s.PgStore.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(p0, p1, p2)
}

func (s *Pg) ClaimNextProjectEnvironmentClone(p0 context.Context, p1 string, p2 time.Duration) (state.ProjectEnvironmentCloneLease, error) {
	if o, ok := s.over.(interface {
		ClaimNextProjectEnvironmentClone(context.Context, string, time.Duration) (state.ProjectEnvironmentCloneLease, error)
	}); ok {
		return o.ClaimNextProjectEnvironmentClone(p0, p1, p2)
	}
	return s.PgStore.ClaimNextProjectEnvironmentClone(p0, p1, p2)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointCaptureRetirement, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(context.Context, state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointCaptureRetirement, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(p0, p1)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(p0, p1)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointObjectCopy(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectCopy, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointObjectCopy(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectCopy, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointObjectCopy(p0, p1, p2, p3)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointObjectCopy(p0, p1, p2, p3)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointObjectRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentCloneCheckpointObjectRetirement, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointObjectRetirement(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentCloneCheckpointObjectRetirement, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointObjectRetirement(p0, p1, p2)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointObjectRetirement(p0, p1, p2)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointObjectTarget(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectTarget, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointObjectTarget(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectTarget, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointObjectTarget(p0, p1, p2, p3)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointObjectTarget(p0, p1, p2, p3)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointObjectVerification(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectVerification, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointObjectVerification(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectVerification, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointObjectVerification(p0, p1, p2, p3)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointObjectVerification(p0, p1, p2, p3)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointPgActivation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 []state.ProjectEnvironmentCloneCheckpointPgConnectionProof) (state.ProjectEnvironmentCloneCheckpointPgActivation, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointPgActivation(context.Context, state.ProjectEnvironmentCloneLease, []state.ProjectEnvironmentCloneCheckpointPgConnectionProof) (state.ProjectEnvironmentCloneCheckpointPgActivation, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointPgActivation(p0, p1, p2)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointPgActivation(p0, p1, p2)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(p0, p1, p2)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(p0, p1, p2)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointPostgresCredential(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointPostgresCredentialProof) (state.ProjectEnvironmentCloneCheckpointPostgresCredential, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointPostgresCredential(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointPostgresCredentialProof) (state.ProjectEnvironmentCloneCheckpointPostgresCredential, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointPostgresCredential(p0, p1, p2, p3)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointPostgresCredential(p0, p1, p2, p3)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(p0, p1, p2)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(p0, p1, p2)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointPostgresIsolation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointPostgresIsolation, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointPostgresIsolation(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointPostgresIsolation, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointPostgresIsolation(p0, p1, p2, p3)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointPostgresIsolation(p0, p1, p2, p3)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointPostgresRuntime(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, p4 state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, p5 state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointPostgresRuntime, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointPostgresRuntime(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointPostgresRuntime, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointPostgresRuntime(p0, p1, p2, p3, p4, p5)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointPostgresRuntime(p0, p1, p2, p3, p4, p5)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointPostgresTargetCompensation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointPostgresTargetCompensation(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointPostgresTargetCompensation(p0, p1, p2)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointPostgresTargetCompensation(p0, p1, p2)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointPostgresVerification(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, p4 state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointPostgresVerification, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointPostgresVerification(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointPostgresVerification, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointPostgresVerification(p0, p1, p2, p3, p4)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointPostgresVerification(p0, p1, p2, p3, p4)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointRelease(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointRelease, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointRelease(context.Context, state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointRelease, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointRelease(p0, p1)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointRelease(p0, p1)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointSourceCompensation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointSourceCompensation, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointSourceCompensation(context.Context, state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointSourceCompensation, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointSourceCompensation(p0, p1)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointSourceCompensation(p0, p1)
}

func (s *Pg) ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointWorkloadRetirement, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(context.Context, state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointWorkloadRetirement, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(p0, p1)
	}
	return s.PgStore.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(p0, p1)
}

func (s *Pg) ClaimProjectEnvironmentClonePostgresArchiveUpload(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 uint32) (state.ProjectEnvironmentClonePostgresArchive, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentClonePostgresArchiveUpload(context.Context, state.ProjectEnvironmentCloneLease, string, uint32) (state.ProjectEnvironmentClonePostgresArchive, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentClonePostgresArchiveUpload(p0, p1, p2, p3)
	}
	return s.PgStore.ClaimProjectEnvironmentClonePostgresArchiveUpload(p0, p1, p2, p3)
}

func (s *Pg) ClaimProjectEnvironmentClonePostgresCopyReaderCleanup(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentClonePostgresCopyReader, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentClonePostgresCopyReaderCleanup(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentClonePostgresCopyReader, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentClonePostgresCopyReaderCleanup(p0, p1, p2)
	}
	return s.PgStore.ClaimProjectEnvironmentClonePostgresCopyReaderCleanup(p0, p1, p2)
}

func (s *Pg) ClaimProjectEnvironmentClonePostgresCopyReaderRequest(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentClonePostgresCopyReader, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentClonePostgresCopyReaderRequest(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentClonePostgresCopyReader, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentClonePostgresCopyReaderRequest(p0, p1, p2)
	}
	return s.PgStore.ClaimProjectEnvironmentClonePostgresCopyReaderRequest(p0, p1, p2)
}

func (s *Pg) ClaimProjectEnvironmentClonePostgresImport(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 uint32) (state.ProjectEnvironmentClonePostgresImport, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentClonePostgresImport(context.Context, state.ProjectEnvironmentCloneLease, string, uint32) (state.ProjectEnvironmentClonePostgresImport, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentClonePostgresImport(p0, p1, p2, p3)
	}
	return s.PgStore.ClaimProjectEnvironmentClonePostgresImport(p0, p1, p2, p3)
}

func (s *Pg) ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentClonePostgresSnapshotRestore, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentClonePostgresSnapshotRestore, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(p0, p1, p2)
	}
	return s.PgStore.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(p0, p1, p2)
}

func (s *Pg) ClaimProjectEnvironmentClonePostgresVerification(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 uint32) (state.ProjectEnvironmentClonePostgresVerification, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentClonePostgresVerification(context.Context, state.ProjectEnvironmentCloneLease, string, uint32) (state.ProjectEnvironmentClonePostgresVerification, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentClonePostgresVerification(p0, p1, p2, p3)
	}
	return s.PgStore.ClaimProjectEnvironmentClonePostgresVerification(p0, p1, p2, p3)
}

func (s *Pg) ClaimProjectEnvironmentClonePostgresVerificationAttempt(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 uint32, p4 string) (state.ProjectEnvironmentClonePostgresVerificationAttempt, bool, error) {
	if o, ok := s.over.(interface {
		ClaimProjectEnvironmentClonePostgresVerificationAttempt(context.Context, state.ProjectEnvironmentCloneLease, string, uint32, string) (state.ProjectEnvironmentClonePostgresVerificationAttempt, bool, error)
	}); ok {
		return o.ClaimProjectEnvironmentClonePostgresVerificationAttempt(p0, p1, p2, p3, p4)
	}
	return s.PgStore.ClaimProjectEnvironmentClonePostgresVerificationAttempt(p0, p1, p2, p3, p4)
}

func (s *Pg) CleanupProjectEnvironmentCloneConfigurationForLease(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) error {
	if o, ok := s.over.(interface {
		CleanupProjectEnvironmentCloneConfigurationForLease(context.Context, state.ProjectEnvironmentCloneLease) error
	}); ok {
		return o.CleanupProjectEnvironmentCloneConfigurationForLease(p0, p1)
	}
	return s.PgStore.CleanupProjectEnvironmentCloneConfigurationForLease(p0, p1)
}

func (s *Pg) CleanupProjectEnvironmentCloneDeploymentsForLease(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) error {
	if o, ok := s.over.(interface {
		CleanupProjectEnvironmentCloneDeploymentsForLease(context.Context, state.ProjectEnvironmentCloneLease) error
	}); ok {
		return o.CleanupProjectEnvironmentCloneDeploymentsForLease(p0, p1)
	}
	return s.PgStore.CleanupProjectEnvironmentCloneDeploymentsForLease(p0, p1)
}

func (s *Pg) CleanupProjectEnvironmentCloneEnvironmentForLease(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) error {
	if o, ok := s.over.(interface {
		CleanupProjectEnvironmentCloneEnvironmentForLease(context.Context, state.ProjectEnvironmentCloneLease) error
	}); ok {
		return o.CleanupProjectEnvironmentCloneEnvironmentForLease(p0, p1)
	}
	return s.PgStore.CleanupProjectEnvironmentCloneEnvironmentForLease(p0, p1)
}

func (s *Pg) CleanupProjectEnvironmentCloneWorkloadsForLease(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) error {
	if o, ok := s.over.(interface {
		CleanupProjectEnvironmentCloneWorkloadsForLease(context.Context, state.ProjectEnvironmentCloneLease) error
	}); ok {
		return o.CleanupProjectEnvironmentCloneWorkloadsForLease(p0, p1)
	}
	return s.PgStore.CleanupProjectEnvironmentCloneWorkloadsForLease(p0, p1)
}

func (s *Pg) CompleteInvocationClaim(p0 context.Context, p1 string, p2 state.InvocationClaim, p3 json.RawMessage) error {
	if o, ok := s.over.(interface {
		CompleteInvocationClaim(context.Context, string, state.InvocationClaim, json.RawMessage) error
	}); ok {
		return o.CompleteInvocationClaim(p0, p1, p2, p3)
	}
	return s.PgStore.CompleteInvocationClaim(p0, p1, p2, p3)
}

func (s *Pg) CompleteProjectEnvironmentCloneCheckpointCompensation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCompensationReceipt, error) {
	if o, ok := s.over.(interface {
		CompleteProjectEnvironmentCloneCheckpointCompensation(context.Context, state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCompensationReceipt, error)
	}); ok {
		return o.CompleteProjectEnvironmentCloneCheckpointCompensation(p0, p1)
	}
	return s.PgStore.CompleteProjectEnvironmentCloneCheckpointCompensation(p0, p1)
}

func (s *Pg) ConsumerKeyByAppAndPrefix(p0 context.Context, p1 string, p2 string, p3 string) (state.ConsumerKey, error) {
	if o, ok := s.over.(interface {
		ConsumerKeyByAppAndPrefix(context.Context, string, string, string) (state.ConsumerKey, error)
	}); ok {
		return o.ConsumerKeyByAppAndPrefix(p0, p1, p2, p3)
	}
	return s.PgStore.ConsumerKeyByAppAndPrefix(p0, p1, p2, p3)
}

func (s *Pg) DispatchProjectEnvironmentCloneObjectGrantRevocation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 grantrevocation.Plan) (state.ProjectEnvironmentCloneObjectGrantRevocation, error) {
	if o, ok := s.over.(interface {
		DispatchProjectEnvironmentCloneObjectGrantRevocation(context.Context, state.ProjectEnvironmentCloneLease, grantrevocation.Plan) (state.ProjectEnvironmentCloneObjectGrantRevocation, error)
	}); ok {
		return o.DispatchProjectEnvironmentCloneObjectGrantRevocation(p0, p1, p2)
	}
	return s.PgStore.DispatchProjectEnvironmentCloneObjectGrantRevocation(p0, p1, p2)
}

func (s *Pg) EnqueueInvocation(p0 context.Context, p1 state.Invocation) (state.Invocation, error) {
	if o, ok := s.over.(interface {
		EnqueueInvocation(context.Context, state.Invocation) (state.Invocation, error)
	}); ok {
		return o.EnqueueInvocation(p0, p1)
	}
	return s.PgStore.EnqueueInvocation(p0, p1)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointCaptureRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) error {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointCaptureRetirement(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) error
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(p0, p1, p2)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(p0, p1, p2)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointObjectCopy(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointObjectCopyObservation, p4 state.ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointObjectCopy(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointObjectCopyObservation, state.ProjectEnvironmentCloneCheckpointReleaseObservation) error
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointObjectCopy(p0, p1, p2, p3, p4)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointObjectCopy(p0, p1, p2, p3, p4)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointObjectRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) error {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointObjectRetirement(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) error
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointObjectRetirement(p0, p1, p2, p3)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointObjectRetirement(p0, p1, p2, p3)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointObjectTarget(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointObjectTargetObservation, p4 state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ObjectBucket, error) {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointObjectTarget(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointObjectTargetObservation, state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ObjectBucket, error)
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointObjectTarget(p0, p1, p2, p3, p4)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointObjectTarget(p0, p1, p2, p3, p4)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointObjectVerification(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation, p4 state.ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointObjectVerification(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation, state.ProjectEnvironmentCloneCheckpointReleaseObservation) error
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointObjectVerification(p0, p1, p2, p3, p4)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointObjectVerification(p0, p1, p2, p3, p4)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointPgActivation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentCloneCheckpointPgActivationObservation, p3 state.ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointPgActivation(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentCloneCheckpointPgActivationObservation, state.ProjectEnvironmentCloneCheckpointReleaseObservation) error
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointPgActivation(p0, p1, p2, p3)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointPgActivation(p0, p1, p2, p3)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) error {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) error
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(p0, p1, p2, p3)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(p0, p1, p2, p3)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointPostgresCredential(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation, p4 state.ProjectEnvironmentCloneCheckpointPostgresCredentialProof) error {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointPostgresCredential(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation, state.ProjectEnvironmentCloneCheckpointPostgresCredentialProof) error
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointPostgresCredential(p0, p1, p2, p3, p4)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointPostgresCredential(p0, p1, p2, p3, p4)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) error {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) error
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(p0, p1, p2, p3)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(p0, p1, p2, p3)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointPostgresIsolation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, p4 state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneDatabaseTarget, error) {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointPostgresIsolation(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneDatabaseTarget, error)
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointPostgresIsolation(p0, p1, p2, p3, p4)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointPostgresIsolation(p0, p1, p2, p3, p4)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointPostgresRuntime(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation, p4 state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, p5 state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, p6 state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneDatabaseTarget, error) {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointPostgresRuntime(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation, state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneDatabaseTarget, error)
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointPostgresRuntime(p0, p1, p2, p3, p4, p5, p6)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointPostgresRuntime(p0, p1, p2, p3, p4, p5, p6)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) error {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) error
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation(p0, p1, p2, p3)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation(p0, p1, p2, p3)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointPostgresVerification(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, p4 state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, p5 state.ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointPostgresVerification(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, state.ProjectEnvironmentCloneCheckpointReleaseObservation) error
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointPostgresVerification(p0, p1, p2, p3, p4, p5)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointPostgresVerification(p0, p1, p2, p3, p4, p5)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointRelease(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneOperation, error) {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointRelease(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneOperation, error)
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointRelease(p0, p1, p2)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointRelease(p0, p1, p2)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointSourceCompensation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation) error {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointSourceCompensation(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation) error
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointSourceCompensation(p0, p1, p2)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointSourceCompensation(p0, p1, p2)
}

func (s *Pg) FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) error {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) error
	}); ok {
		return o.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(p0, p1, p2)
	}
	return s.PgStore.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(p0, p1, p2)
}

func (s *Pg) FinishProjectEnvironmentClonePostgresCopyReaderCleanup(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentClonePostgresCopyReaderDeletion) (state.ProjectEnvironmentClonePostgresCopyReader, error) {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentClonePostgresCopyReaderCleanup(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentClonePostgresCopyReaderDeletion) (state.ProjectEnvironmentClonePostgresCopyReader, error)
	}); ok {
		return o.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(p0, p1, p2, p3)
	}
	return s.PgStore.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(p0, p1, p2, p3)
}

func (s *Pg) FinishProjectEnvironmentClonePostgresCopyTargetCleanup(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentClonePostgresCopyTargetDeletion) (state.ProjectEnvironmentClonePostgresCopyTarget, error) {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentClonePostgresCopyTargetCleanup(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentClonePostgresCopyTargetDeletion) (state.ProjectEnvironmentClonePostgresCopyTarget, error)
	}); ok {
		return o.FinishProjectEnvironmentClonePostgresCopyTargetCleanup(p0, p1, p2, p3)
	}
	return s.PgStore.FinishProjectEnvironmentClonePostgresCopyTargetCleanup(p0, p1, p2, p3)
}

func (s *Pg) FinishProjectEnvironmentClonePostgresSnapshotCleanup(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentClonePostgresSnapshot, error) {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentClonePostgresSnapshotCleanup(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentClonePostgresSnapshot, error)
	}); ok {
		return o.FinishProjectEnvironmentClonePostgresSnapshotCleanup(p0, p1, p2)
	}
	return s.PgStore.FinishProjectEnvironmentClonePostgresSnapshotCleanup(p0, p1, p2)
}

func (s *Pg) FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error)
	}); ok {
		return o.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(p0, p1, p2, p3)
	}
	return s.PgStore.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(p0, p1, p2, p3)
}

func (s *Pg) FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentClonePostgresFenceAbandonment) (state.ProjectEnvironmentClonePostgresWriteFence, error) {
	if o, ok := s.over.(interface {
		FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentClonePostgresFenceAbandonment) (state.ProjectEnvironmentClonePostgresWriteFence, error)
	}); ok {
		return o.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(p0, p1, p2, p3)
	}
	return s.PgStore.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(p0, p1, p2, p3)
}

func (s *Pg) GetAPIConsumerByID(p0 context.Context, p1 string, p2 string) (state.APIConsumer, error) {
	if o, ok := s.over.(interface {
		GetAPIConsumerByID(context.Context, string, string) (state.APIConsumer, error)
	}); ok {
		return o.GetAPIConsumerByID(p0, p1, p2)
	}
	return s.PgStore.GetAPIConsumerByID(p0, p1, p2)
}

func (s *Pg) MaterializeProjectEnvironmentCloneForLease(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 []string, p3 int, p4 api.Limits) (state.ProjectEnvironment, error) {
	if o, ok := s.over.(interface {
		MaterializeProjectEnvironmentCloneForLease(context.Context, state.ProjectEnvironmentCloneLease, []string, int, api.Limits) (state.ProjectEnvironment, error)
	}); ok {
		return o.MaterializeProjectEnvironmentCloneForLease(p0, p1, p2, p3, p4)
	}
	return s.PgStore.MaterializeProjectEnvironmentCloneForLease(p0, p1, p2, p3, p4)
}

func (s *Pg) PrepareProjectEnvironmentCloneCheckpointObjectCredentialValues(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentCloneCheckpointObjectCredentialValues, p3 state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectCredentialValues, error) {
	if o, ok := s.over.(interface {
		PrepareProjectEnvironmentCloneCheckpointObjectCredentialValues(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentCloneCheckpointObjectCredentialValues, state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectCredentialValues, error)
	}); ok {
		return o.PrepareProjectEnvironmentCloneCheckpointObjectCredentialValues(p0, p1, p2, p3)
	}
	return s.PgStore.PrepareProjectEnvironmentCloneCheckpointObjectCredentialValues(p0, p1, p2, p3)
}

func (s *Pg) PrepareProjectEnvironmentClonePostgresBinding(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentClonePostgresBindingRequest) (state.ProjectEnvironmentClonePostgresBindingPreparation, error) {
	if o, ok := s.over.(interface {
		PrepareProjectEnvironmentClonePostgresBinding(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresBindingRequest) (state.ProjectEnvironmentClonePostgresBindingPreparation, error)
	}); ok {
		return o.PrepareProjectEnvironmentClonePostgresBinding(p0, p1, p2)
	}
	return s.PgStore.PrepareProjectEnvironmentClonePostgresBinding(p0, p1, p2)
}

func (s *Pg) ProjectEnvironmentCloneConfigurationFenceForLease(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneConfigurationFence, error) {
	if o, ok := s.over.(interface {
		ProjectEnvironmentCloneConfigurationFenceForLease(context.Context, state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneConfigurationFence, error)
	}); ok {
		return o.ProjectEnvironmentCloneConfigurationFenceForLease(p0, p1)
	}
	return s.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(p0, p1)
}

func (s *Pg) ProjectEnvironmentCloneObjectWriteFencesForLease(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) ([]state.ObjectBucketWriteFence, error) {
	if o, ok := s.over.(interface {
		ProjectEnvironmentCloneObjectWriteFencesForLease(context.Context, state.ProjectEnvironmentCloneLease) ([]state.ObjectBucketWriteFence, error)
	}); ok {
		return o.ProjectEnvironmentCloneObjectWriteFencesForLease(p0, p1)
	}
	return s.PgStore.ProjectEnvironmentCloneObjectWriteFencesForLease(p0, p1)
}

func (s *Pg) ProjectEnvironmentClonePostgresContentsForLease(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 uint32) (state.ProjectEnvironmentClonePostgresContents, error) {
	if o, ok := s.over.(interface {
		ProjectEnvironmentClonePostgresContentsForLease(context.Context, state.ProjectEnvironmentCloneLease, string, uint32) (state.ProjectEnvironmentClonePostgresContents, error)
	}); ok {
		return o.ProjectEnvironmentClonePostgresContentsForLease(p0, p1, p2, p3)
	}
	return s.PgStore.ProjectEnvironmentClonePostgresContentsForLease(p0, p1, p2, p3)
}

func (s *Pg) ProjectEnvironmentClonePostgresDatabasePlanForLease(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentClonePostgresDatabasePlan, error) {
	if o, ok := s.over.(interface {
		ProjectEnvironmentClonePostgresDatabasePlanForLease(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentClonePostgresDatabasePlan, error)
	}); ok {
		return o.ProjectEnvironmentClonePostgresDatabasePlanForLease(p0, p1, p2)
	}
	return s.PgStore.ProjectEnvironmentClonePostgresDatabasePlanForLease(p0, p1, p2)
}

func (s *Pg) ProjectEnvironmentClonePostgresWriteFencesForLease(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) ([]state.ProjectEnvironmentClonePostgresWriteFence, error) {
	if o, ok := s.over.(interface {
		ProjectEnvironmentClonePostgresWriteFencesForLease(context.Context, state.ProjectEnvironmentCloneLease) ([]state.ProjectEnvironmentClonePostgresWriteFence, error)
	}); ok {
		return o.ProjectEnvironmentClonePostgresWriteFencesForLease(p0, p1)
	}
	return s.PgStore.ProjectEnvironmentClonePostgresWriteFencesForLease(p0, p1)
}

func (s *Pg) PublishProjectEnvironmentCloneCheckpointObjects(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectPublication, error) {
	if o, ok := s.over.(interface {
		PublishProjectEnvironmentCloneCheckpointObjects(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectPublication, error)
	}); ok {
		return o.PublishProjectEnvironmentCloneCheckpointObjects(p0, p1, p2, p3)
	}
	return s.PgStore.PublishProjectEnvironmentCloneCheckpointObjects(p0, p1, p2, p3)
}

func (s *Pg) PublishProjectEnvironmentCloneCheckpointPgBindings(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 []state.AppSecret, p3 state.ProjectEnvironmentCloneCheckpointPgActivationObservation, p4 state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointPgPublication, error) {
	if o, ok := s.over.(interface {
		PublishProjectEnvironmentCloneCheckpointPgBindings(context.Context, state.ProjectEnvironmentCloneLease, []state.AppSecret, state.ProjectEnvironmentCloneCheckpointPgActivationObservation, state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointPgPublication, error)
	}); ok {
		return o.PublishProjectEnvironmentCloneCheckpointPgBindings(p0, p1, p2, p3, p4)
	}
	return s.PgStore.PublishProjectEnvironmentCloneCheckpointPgBindings(p0, p1, p2, p3, p4)
}

func (s *Pg) RecordProjectEnvironmentCloneObjectGrantRevocation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 grantrevocation.Plan, p3 grantrevocation.Observation) (state.ProjectEnvironmentCloneObjectGrantRevocation, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentCloneObjectGrantRevocation(context.Context, state.ProjectEnvironmentCloneLease, grantrevocation.Plan, grantrevocation.Observation) (state.ProjectEnvironmentCloneObjectGrantRevocation, error)
	}); ok {
		return o.RecordProjectEnvironmentCloneObjectGrantRevocation(p0, p1, p2, p3)
	}
	return s.PgStore.RecordProjectEnvironmentCloneObjectGrantRevocation(p0, p1, p2, p3)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresArchive(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 uint32, p4 copyarchive.Receipt) (state.ProjectEnvironmentClonePostgresArchive, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresArchive(context.Context, state.ProjectEnvironmentCloneLease, string, uint32, copyarchive.Receipt) (state.ProjectEnvironmentClonePostgresArchive, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresArchive(p0, p1, p2, p3, p4)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresArchive(p0, p1, p2, p3, p4)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresCheckpointSelection(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 checkpointselection.Sealed) (state.ProjectEnvironmentClonePostgresCheckpointSelection, bool, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresCheckpointSelection(context.Context, state.ProjectEnvironmentCloneLease, string, checkpointselection.Sealed) (state.ProjectEnvironmentClonePostgresCheckpointSelection, bool, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresCheckpointSelection(p0, p1, p2, p3)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresCheckpointSelection(p0, p1, p2, p3)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresContents(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 uint32, p4 copycontents.Sealed) (state.ProjectEnvironmentClonePostgresContents, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresContents(context.Context, state.ProjectEnvironmentCloneLease, string, uint32, copycontents.Sealed) (state.ProjectEnvironmentClonePostgresContents, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresContents(p0, p1, p2, p3, p4)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresContents(p0, p1, p2, p3, p4)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresCopyReader(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentClonePostgresCopyReaderObservation) (state.ProjectEnvironmentClonePostgresCopyReader, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresCopyReader(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentClonePostgresCopyReaderObservation) (state.ProjectEnvironmentClonePostgresCopyReader, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresCopyReader(p0, p1, p2, p3)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresCopyReader(p0, p1, p2, p3)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentClonePostgresCopyReaderDeletion) (state.ProjectEnvironmentClonePostgresCopyReader, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentClonePostgresCopyReaderDeletion) (state.ProjectEnvironmentClonePostgresCopyReader, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(p0, p1, p2, p3)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(p0, p1, p2, p3)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresCopyTarget(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentClonePostgresCopyTargetObservation) (state.ProjectEnvironmentClonePostgresCopyTarget, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresCopyTarget(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentClonePostgresCopyTargetObservation) (state.ProjectEnvironmentClonePostgresCopyTarget, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresCopyTarget(p0, p1, p2, p3)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresCopyTarget(p0, p1, p2, p3)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentity(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentClonePostgresCopyTargetDeletion) (state.ProjectEnvironmentClonePostgresCopyTarget, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentity(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentClonePostgresCopyTargetDeletion) (state.ProjectEnvironmentClonePostgresCopyTarget, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentity(p0, p1, p2, p3)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentity(p0, p1, p2, p3)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresDatabasePlan(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 string, p4 copydatabases.Sealed) (state.ProjectEnvironmentClonePostgresDatabasePlan, bool, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresDatabasePlan(context.Context, state.ProjectEnvironmentCloneLease, string, string, copydatabases.Sealed) (state.ProjectEnvironmentClonePostgresDatabasePlan, bool, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresDatabasePlan(p0, p1, p2, p3, p4)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresDatabasePlan(p0, p1, p2, p3, p4)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresDatabaseSQLPins(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 uint32, p4 string, p5 string, p6 copydatabases.SealedPreparation) (state.ProjectEnvironmentClonePostgresDatabaseSQLPins, bool, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresDatabaseSQLPins(context.Context, state.ProjectEnvironmentCloneLease, string, uint32, string, string, copydatabases.SealedPreparation) (state.ProjectEnvironmentClonePostgresDatabaseSQLPins, bool, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresDatabaseSQLPins(p0, p1, p2, p3, p4, p5, p6)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresDatabaseSQLPins(p0, p1, p2, p3, p4, p5, p6)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresImportExecution(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 uint32, p4 copyarchive.RestoreExecution) (state.ProjectEnvironmentClonePostgresImport, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresImportExecution(context.Context, state.ProjectEnvironmentCloneLease, string, uint32, copyarchive.RestoreExecution) (state.ProjectEnvironmentClonePostgresImport, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresImportExecution(p0, p1, p2, p3, p4)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresImportExecution(p0, p1, p2, p3, p4)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresInventory(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 copyinventory.Sealed) (state.ProjectEnvironmentClonePostgresInventory, bool, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresInventory(context.Context, state.ProjectEnvironmentCloneLease, string, copyinventory.Sealed) (state.ProjectEnvironmentClonePostgresInventory, bool, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresInventory(p0, p1, p2, p3)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresInventory(p0, p1, p2, p3)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresMaintenance(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 string, p4 state.ProjectEnvironmentClonePostgresMaintenanceObservation) (state.ProjectEnvironmentClonePostgresMaintenance, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresMaintenance(context.Context, state.ProjectEnvironmentCloneLease, string, string, state.ProjectEnvironmentClonePostgresMaintenanceObservation) (state.ProjectEnvironmentClonePostgresMaintenance, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresMaintenance(p0, p1, p2, p3, p4)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresMaintenance(p0, p1, p2, p3, p4)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresMembershipPlan(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 string, p4 copyroles.SealedMemberships) (state.ProjectEnvironmentClonePostgresMembershipPlan, bool, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresMembershipPlan(context.Context, state.ProjectEnvironmentCloneLease, string, string, copyroles.SealedMemberships) (state.ProjectEnvironmentClonePostgresMembershipPlan, bool, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresMembershipPlan(p0, p1, p2, p3, p4)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresMembershipPlan(p0, p1, p2, p3, p4)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresRolePlan(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 copyroles.Sealed) (state.ProjectEnvironmentClonePostgresRolePlan, bool, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresRolePlan(context.Context, state.ProjectEnvironmentCloneLease, string, copyroles.Sealed) (state.ProjectEnvironmentClonePostgresRolePlan, bool, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresRolePlan(p0, p1, p2, p3)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresRolePlan(p0, p1, p2, p3)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresSnapshot(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentClonePostgresSnapshotObservation) (state.ProjectEnvironmentClonePostgresSnapshot, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresSnapshot(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentClonePostgresSnapshotObservation) (state.ProjectEnvironmentClonePostgresSnapshot, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresSnapshot(p0, p1, p2, p3)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresSnapshot(p0, p1, p2, p3)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresSnapshotRestore(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentClonePostgresSnapshotRestoreObservation) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresSnapshotRestore(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentClonePostgresSnapshotRestoreObservation) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresSnapshotRestore(p0, p1, p2, p3)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresSnapshotRestore(p0, p1, p2, p3)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentClonePostgresSnapshotRestoreObservation) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentClonePostgresSnapshotRestoreObservation) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(p0, p1, p2, p3)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(p0, p1, p2, p3)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(p0, p1, p2, p3)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(p0, p1, p2, p3)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresTargetSQLPins(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 string, p4 copyarchive.SealedTarget) (state.ProjectEnvironmentClonePostgresTargetSQLPins, bool, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresTargetSQLPins(context.Context, state.ProjectEnvironmentCloneLease, string, string, copyarchive.SealedTarget) (state.ProjectEnvironmentClonePostgresTargetSQLPins, bool, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresTargetSQLPins(p0, p1, p2, p3, p4)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresTargetSQLPins(p0, p1, p2, p3, p4)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresVerificationAttemptClosure(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 uint32, p4 string, p5 state.ProjectEnvironmentClonePostgresVerificationCompletion) (state.ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresVerificationAttemptClosure(context.Context, state.ProjectEnvironmentCloneLease, string, uint32, string, state.ProjectEnvironmentClonePostgresVerificationCompletion) (state.ProjectEnvironmentClonePostgresVerificationAttempt, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresVerificationAttemptClosure(p0, p1, p2, p3, p4, p5)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresVerificationAttemptClosure(p0, p1, p2, p3, p4, p5)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 uint32, p4 string, p5 copycontents.SealedMatch) (state.ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(context.Context, state.ProjectEnvironmentCloneLease, string, uint32, string, copycontents.SealedMatch) (state.ProjectEnvironmentClonePostgresVerificationAttempt, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(p0, p1, p2, p3, p4, p5)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(p0, p1, p2, p3, p4, p5)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresVerificationClosure(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 uint32, p4 state.ProjectEnvironmentClonePostgresVerificationCompletion) (state.ProjectEnvironmentClonePostgresVerification, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresVerificationClosure(context.Context, state.ProjectEnvironmentCloneLease, string, uint32, state.ProjectEnvironmentClonePostgresVerificationCompletion) (state.ProjectEnvironmentClonePostgresVerification, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresVerificationClosure(p0, p1, p2, p3, p4)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresVerificationClosure(p0, p1, p2, p3, p4)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresVerificationFailure(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 uint32, p4 string, p5 state.ProjectEnvironmentClonePostgresVerificationFailure) (state.ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresVerificationFailure(context.Context, state.ProjectEnvironmentCloneLease, string, uint32, string, state.ProjectEnvironmentClonePostgresVerificationFailure) (state.ProjectEnvironmentClonePostgresVerificationAttempt, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresVerificationFailure(p0, p1, p2, p3, p4, p5)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresVerificationFailure(p0, p1, p2, p3, p4, p5)
}

func (s *Pg) RecordProjectEnvironmentClonePostgresVerificationMatch(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 uint32, p4 copycontents.SealedMatch) (state.ProjectEnvironmentClonePostgresVerification, error) {
	if o, ok := s.over.(interface {
		RecordProjectEnvironmentClonePostgresVerificationMatch(context.Context, state.ProjectEnvironmentCloneLease, string, uint32, copycontents.SealedMatch) (state.ProjectEnvironmentClonePostgresVerification, error)
	}); ok {
		return o.RecordProjectEnvironmentClonePostgresVerificationMatch(p0, p1, p2, p3, p4)
	}
	return s.PgStore.RecordProjectEnvironmentClonePostgresVerificationMatch(p0, p1, p2, p3, p4)
}

func (s *Pg) ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) error {
	if o, ok := s.over.(interface {
		ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(context.Context, state.ProjectEnvironmentCloneLease, string) error
	}); ok {
		return o.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(p0, p1, p2)
	}
	return s.PgStore.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(p0, p1, p2)
}

func (s *Pg) ReleaseProjectEnvironmentCloneLease(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 time.Duration) error {
	if o, ok := s.over.(interface {
		ReleaseProjectEnvironmentCloneLease(context.Context, state.ProjectEnvironmentCloneLease, time.Duration) error
	}); ok {
		return o.ReleaseProjectEnvironmentCloneLease(p0, p1, p2)
	}
	return s.PgStore.ReleaseProjectEnvironmentCloneLease(p0, p1, p2)
}

func (s *Pg) RenewProjectEnvironmentCloneLease(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 time.Duration) (state.ProjectEnvironmentCloneLease, error) {
	if o, ok := s.over.(interface {
		RenewProjectEnvironmentCloneLease(context.Context, state.ProjectEnvironmentCloneLease, time.Duration) (state.ProjectEnvironmentCloneLease, error)
	}); ok {
		return o.RenewProjectEnvironmentCloneLease(p0, p1, p2)
	}
	return s.PgStore.RenewProjectEnvironmentCloneLease(p0, p1, p2)
}

func (s *Pg) ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointCaptureRetirement, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(context.Context, state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointCaptureRetirement, error)
	}); ok {
		return o.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(p0, p1)
	}
	return s.PgStore.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(p0, p1)
}

func (s *Pg) ReserveProjectEnvironmentCloneCheckpointObjectRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentCloneCheckpointObjectRetirement, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentCloneCheckpointObjectRetirement(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentCloneCheckpointObjectRetirement, error)
	}); ok {
		return o.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(p0, p1, p2)
	}
	return s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(p0, p1, p2)
}

func (s *Pg) ReserveProjectEnvironmentCloneCheckpointPgActivation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 []state.ProjectEnvironmentCloneCheckpointPgConnectionProof) (state.ProjectEnvironmentCloneCheckpointPgActivation, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentCloneCheckpointPgActivation(context.Context, state.ProjectEnvironmentCloneLease, []state.ProjectEnvironmentCloneCheckpointPgConnectionProof) (state.ProjectEnvironmentCloneCheckpointPgActivation, error)
	}); ok {
		return o.ReserveProjectEnvironmentCloneCheckpointPgActivation(p0, p1, p2)
	}
	return s.PgStore.ReserveProjectEnvironmentCloneCheckpointPgActivation(p0, p1, p2)
}

func (s *Pg) ReserveProjectEnvironmentCloneCheckpointPgConnectionValue(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 state.ProjectEnvironmentCloneCheckpointPgConnectionProof) (state.ProjectEnvironmentCloneCheckpointPgConnectionValue, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentCloneCheckpointPgConnectionValue(context.Context, state.ProjectEnvironmentCloneLease, string, state.ProjectEnvironmentCloneCheckpointPgConnectionProof) (state.ProjectEnvironmentCloneCheckpointPgConnectionValue, error)
	}); ok {
		return o.ReserveProjectEnvironmentCloneCheckpointPgConnectionValue(p0, p1, p2, p3)
	}
	return s.PgStore.ReserveProjectEnvironmentCloneCheckpointPgConnectionValue(p0, p1, p2, p3)
}

func (s *Pg) ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, error)
	}); ok {
		return o.ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement(p0, p1, p2)
	}
	return s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement(p0, p1, p2)
}

func (s *Pg) ReserveProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement, error)
	}); ok {
		return o.ReserveProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(p0, p1, p2)
	}
	return s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(p0, p1, p2)
}

func (s *Pg) ReserveProjectEnvironmentCloneCheckpointPostgresTargetCompensation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentCloneCheckpointPostgresTargetCompensation(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, error)
	}); ok {
		return o.ReserveProjectEnvironmentCloneCheckpointPostgresTargetCompensation(p0, p1, p2)
	}
	return s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresTargetCompensation(p0, p1, p2)
}

func (s *Pg) ReserveProjectEnvironmentCloneCheckpointSourceCompensation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan) (state.ProjectEnvironmentCloneCheckpointSourceCompensation, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentCloneCheckpointSourceCompensation(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan) (state.ProjectEnvironmentCloneCheckpointSourceCompensation, error)
	}); ok {
		return o.ReserveProjectEnvironmentCloneCheckpointSourceCompensation(p0, p1, p2)
	}
	return s.PgStore.ReserveProjectEnvironmentCloneCheckpointSourceCompensation(p0, p1, p2)
}

func (s *Pg) ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointWorkloadRetirement, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(context.Context, state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointWorkloadRetirement, error)
	}); ok {
		return o.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(p0, p1)
	}
	return s.PgStore.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(p0, p1)
}

func (s *Pg) ReserveProjectEnvironmentCloneDatabase(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 int) (state.ProjectEnvironmentCloneDatabaseTarget, bool, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentCloneDatabase(context.Context, state.ProjectEnvironmentCloneLease, string, int) (state.ProjectEnvironmentCloneDatabaseTarget, bool, error)
	}); ok {
		return o.ReserveProjectEnvironmentCloneDatabase(p0, p1, p2, p3)
	}
	return s.PgStore.ReserveProjectEnvironmentCloneDatabase(p0, p1, p2, p3)
}

func (s *Pg) ReserveProjectEnvironmentCloneObjectGrantRevocation(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentCloneObjectGrantRevocation, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentCloneObjectGrantRevocation(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentCloneObjectGrantRevocation, error)
	}); ok {
		return o.ReserveProjectEnvironmentCloneObjectGrantRevocation(p0, p1, p2)
	}
	return s.PgStore.ReserveProjectEnvironmentCloneObjectGrantRevocation(p0, p1, p2)
}

func (s *Pg) ReserveProjectEnvironmentClonePostgresArchive(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentClonePostgresArchiveRequest, p3 state.ProjectEnvironmentClonePostgresArchiveLimits) (state.ProjectEnvironmentClonePostgresArchive, bool, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentClonePostgresArchive(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresArchiveRequest, state.ProjectEnvironmentClonePostgresArchiveLimits) (state.ProjectEnvironmentClonePostgresArchive, bool, error)
	}); ok {
		return o.ReserveProjectEnvironmentClonePostgresArchive(p0, p1, p2, p3)
	}
	return s.PgStore.ReserveProjectEnvironmentClonePostgresArchive(p0, p1, p2, p3)
}

func (s *Pg) ReserveProjectEnvironmentClonePostgresBinding(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentClonePostgresBindingTarget, bool, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentClonePostgresBinding(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentClonePostgresBindingTarget, bool, error)
	}); ok {
		return o.ReserveProjectEnvironmentClonePostgresBinding(p0, p1, p2)
	}
	return s.PgStore.ReserveProjectEnvironmentClonePostgresBinding(p0, p1, p2)
}

func (s *Pg) ReserveProjectEnvironmentClonePostgresContents(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentClonePostgresContentsRequest, p3 state.ProjectEnvironmentClonePostgresContentsLimits) (state.ProjectEnvironmentClonePostgresContents, bool, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentClonePostgresContents(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresContentsRequest, state.ProjectEnvironmentClonePostgresContentsLimits) (state.ProjectEnvironmentClonePostgresContents, bool, error)
	}); ok {
		return o.ReserveProjectEnvironmentClonePostgresContents(p0, p1, p2, p3)
	}
	return s.PgStore.ReserveProjectEnvironmentClonePostgresContents(p0, p1, p2, p3)
}

func (s *Pg) ReserveProjectEnvironmentClonePostgresCopyReader(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 int) (state.ProjectEnvironmentClonePostgresCopyReader, bool, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentClonePostgresCopyReader(context.Context, state.ProjectEnvironmentCloneLease, string, int) (state.ProjectEnvironmentClonePostgresCopyReader, bool, error)
	}); ok {
		return o.ReserveProjectEnvironmentClonePostgresCopyReader(p0, p1, p2, p3)
	}
	return s.PgStore.ReserveProjectEnvironmentClonePostgresCopyReader(p0, p1, p2, p3)
}

func (s *Pg) ReserveProjectEnvironmentClonePostgresCopyTarget(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 int) (state.ProjectEnvironmentClonePostgresCopyTarget, bool, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentClonePostgresCopyTarget(context.Context, state.ProjectEnvironmentCloneLease, string, int) (state.ProjectEnvironmentClonePostgresCopyTarget, bool, error)
	}); ok {
		return o.ReserveProjectEnvironmentClonePostgresCopyTarget(p0, p1, p2, p3)
	}
	return s.PgStore.ReserveProjectEnvironmentClonePostgresCopyTarget(p0, p1, p2, p3)
}

func (s *Pg) ReserveProjectEnvironmentClonePostgresImport(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentClonePostgresImportRequest) (state.ProjectEnvironmentClonePostgresImport, bool, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentClonePostgresImport(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresImportRequest) (state.ProjectEnvironmentClonePostgresImport, bool, error)
	}); ok {
		return o.ReserveProjectEnvironmentClonePostgresImport(p0, p1, p2)
	}
	return s.PgStore.ReserveProjectEnvironmentClonePostgresImport(p0, p1, p2)
}

func (s *Pg) ReserveProjectEnvironmentClonePostgresMaintenance(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentClonePostgresMaintenance, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentClonePostgresMaintenance(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentClonePostgresMaintenance, error)
	}); ok {
		return o.ReserveProjectEnvironmentClonePostgresMaintenance(p0, p1, p2)
	}
	return s.PgStore.ReserveProjectEnvironmentClonePostgresMaintenance(p0, p1, p2)
}

func (s *Pg) ReserveProjectEnvironmentClonePostgresSnapshot(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string) (state.ProjectEnvironmentClonePostgresSnapshot, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentClonePostgresSnapshot(context.Context, state.ProjectEnvironmentCloneLease, string) (state.ProjectEnvironmentClonePostgresSnapshot, error)
	}); ok {
		return o.ReserveProjectEnvironmentClonePostgresSnapshot(p0, p1, p2)
	}
	return s.PgStore.ReserveProjectEnvironmentClonePostgresSnapshot(p0, p1, p2)
}

func (s *Pg) ReserveProjectEnvironmentClonePostgresSnapshotRestore(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 int) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentClonePostgresSnapshotRestore(context.Context, state.ProjectEnvironmentCloneLease, string, int) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error)
	}); ok {
		return o.ReserveProjectEnvironmentClonePostgresSnapshotRestore(p0, p1, p2, p3)
	}
	return s.PgStore.ReserveProjectEnvironmentClonePostgresSnapshotRestore(p0, p1, p2, p3)
}

func (s *Pg) ReserveProjectEnvironmentClonePostgresVerification(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentClonePostgresVerificationRequest) (state.ProjectEnvironmentClonePostgresVerification, bool, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentClonePostgresVerification(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresVerificationRequest) (state.ProjectEnvironmentClonePostgresVerification, bool, error)
	}); ok {
		return o.ReserveProjectEnvironmentClonePostgresVerification(p0, p1, p2)
	}
	return s.PgStore.ReserveProjectEnvironmentClonePostgresVerification(p0, p1, p2)
}

func (s *Pg) ReserveProjectEnvironmentClonePostgresVerificationReadBudget(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentClonePostgresVerificationReadBudgetRequest, p3 state.ProjectEnvironmentClonePostgresVerificationReadBudgetLimits) (state.ProjectEnvironmentClonePostgresVerificationReadBudget, bool, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentClonePostgresVerificationReadBudget(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresVerificationReadBudgetRequest, state.ProjectEnvironmentClonePostgresVerificationReadBudgetLimits) (state.ProjectEnvironmentClonePostgresVerificationReadBudget, bool, error)
	}); ok {
		return o.ReserveProjectEnvironmentClonePostgresVerificationReadBudget(p0, p1, p2, p3)
	}
	return s.PgStore.ReserveProjectEnvironmentClonePostgresVerificationReadBudget(p0, p1, p2, p3)
}

func (s *Pg) ReserveProjectEnvironmentClonePostgresVerificationRetry(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 state.ProjectEnvironmentClonePostgresVerificationRetryRequest) (state.ProjectEnvironmentClonePostgresVerificationAttempt, bool, error) {
	if o, ok := s.over.(interface {
		ReserveProjectEnvironmentClonePostgresVerificationRetry(context.Context, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresVerificationRetryRequest) (state.ProjectEnvironmentClonePostgresVerificationAttempt, bool, error)
	}); ok {
		return o.ReserveProjectEnvironmentClonePostgresVerificationRetry(p0, p1, p2)
	}
	return s.PgStore.ReserveProjectEnvironmentClonePostgresVerificationRetry(p0, p1, p2)
}

func (s *Pg) SealProjectEnvironmentCloneCheckpointPgConnectionValue(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 string, p4 []byte, p5 state.ProjectEnvironmentCloneCheckpointPgConnectionProof) (state.ProjectEnvironmentCloneCheckpointPgConnectionValue, error) {
	if o, ok := s.over.(interface {
		SealProjectEnvironmentCloneCheckpointPgConnectionValue(context.Context, state.ProjectEnvironmentCloneLease, string, string, []byte, state.ProjectEnvironmentCloneCheckpointPgConnectionProof) (state.ProjectEnvironmentCloneCheckpointPgConnectionValue, error)
	}); ok {
		return o.SealProjectEnvironmentCloneCheckpointPgConnectionValue(p0, p1, p2, p3, p4, p5)
	}
	return s.PgStore.SealProjectEnvironmentCloneCheckpointPgConnectionValue(p0, p1, p2, p3, p4, p5)
}

func (s *Pg) SealProjectEnvironmentCloneCheckpointPostgresCredential(p0 context.Context, p1 state.ProjectEnvironmentCloneLease, p2 string, p3 string, p4 []byte) (state.ProjectEnvironmentCloneCheckpointPostgresCredential, error) {
	if o, ok := s.over.(interface {
		SealProjectEnvironmentCloneCheckpointPostgresCredential(context.Context, state.ProjectEnvironmentCloneLease, string, string, []byte) (state.ProjectEnvironmentCloneCheckpointPostgresCredential, error)
	}); ok {
		return o.SealProjectEnvironmentCloneCheckpointPostgresCredential(p0, p1, p2, p3, p4)
	}
	return s.PgStore.SealProjectEnvironmentCloneCheckpointPostgresCredential(p0, p1, p2, p3, p4)
}

func (s *Pg) TouchConsumerKeyLastUsed(p0 context.Context, p1 string) error {
	if o, ok := s.over.(interface {
		TouchConsumerKeyLastUsed(context.Context, string) error
	}); ok {
		return o.TouchConsumerKeyLastUsed(p0, p1)
	}
	return s.PgStore.TouchConsumerKeyLastUsed(p0, p1)
}

func (s *Pg) WithdrawProjectEnvironmentCloneCheckpointObjects(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) error {
	if o, ok := s.over.(interface {
		WithdrawProjectEnvironmentCloneCheckpointObjects(context.Context, state.ProjectEnvironmentCloneLease) error
	}); ok {
		return o.WithdrawProjectEnvironmentCloneCheckpointObjects(p0, p1)
	}
	return s.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(p0, p1)
}

func (s *Pg) MigrateInstanceOwnerWithRuntimeConfig(ctx context.Context, id, from, to, lease string, input state.RuntimeConfigMigration) error {
	if o, ok := s.over.(interface {
		MigrateInstanceOwnerWithRuntimeConfig(context.Context, string, string, string, string, state.RuntimeConfigMigration) error
	}); ok {
		return o.MigrateInstanceOwnerWithRuntimeConfig(ctx, id, from, to, lease, input)
	}
	return s.PgStore.MigrateInstanceOwnerWithRuntimeConfig(ctx, id, from, to, lease, input)
}

func (s *Pg) ResolveInstanceMigrationCommit(ctx context.Context, attempt state.MigrationCommitAttempt) (state.MigrationCommitResolution, error) {
	if o, ok := s.over.(interface {
		ResolveInstanceMigrationCommit(context.Context, state.MigrationCommitAttempt) (state.MigrationCommitResolution, error)
	}); ok {
		return o.ResolveInstanceMigrationCommit(ctx, attempt)
	}
	return s.PgStore.ResolveInstanceMigrationCommit(ctx, attempt)
}

func (s *Pg) WithdrawProjectEnvironmentCloneCheckpointPgBindings(p0 context.Context, p1 state.ProjectEnvironmentCloneLease) error {
	if o, ok := s.over.(interface {
		WithdrawProjectEnvironmentCloneCheckpointPgBindings(context.Context, state.ProjectEnvironmentCloneLease) error
	}); ok {
		return o.WithdrawProjectEnvironmentCloneCheckpointPgBindings(p0, p1)
	}
	return s.PgStore.WithdrawProjectEnvironmentCloneCheckpointPgBindings(p0, p1)
}

// storeDispatched lists the methods Store forwards to its override.
var storeDispatched = map[string]bool{
	"ListAllApps":                                   true,
	"ListAllInstances":                              true,
	"OperatorAlertCollectionHistory":                true,
	"OperatorAlertCollectionStatus":                 true,
	"OperatorAlertEpisode":                          true,
	"OperatorAlertEpisodes":                         true,
	"OperatorAlertRecurrence":                       true,
	"OperatorAlertResponseTimes":                    true,
	"OperatorAlertRetentionStatus":                  true,
	"OperatorAlertTriageHistory":                    true,
	"OperatorAlertWorkload":                         true,
	"OperatorAuditTraceCoverage":                    true,
	"OperatorCapacityProfiles":                      true,
	"OperatorIncidentContext":                       true,
	"OperatorOverview":                              true,
	"OperatorTenantApps":                            true,
	"RecordOperatorAlertSnapshot":                   true,
	"ScenarioWebhookDeliveryBlocked":                true,
	"SubmitOperatorRecovery":                        true,
	"MCPPolicyConvergenceSnapshot":                  true,
	"LatestAccountEdgeRulePolicyPosition":           true,
	"LatestEdgeRuleChangeID":                        true,
	"GetAppScalingPolicyStatus":                     true,
	"ListServingAppCPUPolicyNodeStates":             true,
	"ListServingAppEgressPolicyNodeStates":          true,
	"ListServingGatewayControlPlaneStates":          true,
	"LatestAppCPUPolicyRevision":                    true,
	"LatestAppEgressPolicyRevision":                 true,
	"LatestAppResponseCachePurgeID":                 true,
	"LatestAccountCorsPresetChangeID":               true,
	"LatestAppEdgeRuleChangeID":                     true,
	"LatestAppRequestPolicyRevision":                true,
	"LatestAppControlPlaneChangeID":                 true,
	"AccountByID":                                   true,
	"AppByID":                                       true,
	"AppendEvent":                                   true,
	"AppendEventWithTrace":                          true,
	"AppendUploadBytes":                             true,
	"ClaimInvocation":                               true,
	"ClaimInvocationWithCap":                        true,
	"ConsumerKeyByAppAndPrefix":                     true,
	"DeploymentByID":                                true,
	"EnqueueInvocation":                             true,
	"GetAPIConsumerByID":                            true,
	"GetAccountOverageCapCents":                     true,
	"GetUploadSession":                              true,
	"InsertOperatorIntent":                          true,
	"InstanceByID":                                  true,
	"JobTaskClaimBatch":                             true,
	"JobTaskMarkClaimed":                            true,
	"ListAppSecretsInScope":                         true,
	"ListApps":                                      true,
	"ListDeploymentsForApp":                         true,
	"ListInstancesForAccount":                       true,
	"ListMirrorRules":                               true,
	"ListOrphanedAppsPage":                          true,
	"ListPublicStatusEvents":                        true,
	"ListRequestTelemetryByPlatformTenant":          true,
	"MarkSnapshotStale":                             true,
	"Ping":                                          true,
	"QueueStateInScope":                             true,
	"ReapExpiredUploadSessions":                     true,
	"RecordAppSecretDelivery":                       true,
	"RuntimeAppValuesForDeployment":                 true,
	"TouchConsumerKeyLastUsed":                      true,
	"UpdateInstanceStateToTerminal":                 true,
	"UpdateProjectEnvironmentPromotionVerification": true,
	"UsageByAccount":                                true,
	"UsageSLOForAccount":                            true,
	"ClaimDueAppWebhookDeliveries":                  true,
	"CreateInstanceWithMode":                        true,
	"ListAppEnvInScope":                             true,
	"ListAppTrustedSignersForApp":                   true,
	"ListDeploymentsForOperator":                    true,
	"ProjectByOrgSlug":                              true,
	"ListProjectsForOrg":                            true,
}

func (s *Store) AccountByID(p0 context.Context, p1 string) (state.Account, error) {
	if o, ok := s.over.(interface {
		AccountByID(context.Context, string) (state.Account, error)
	}); ok {
		return o.AccountByID(p0, p1)
	}
	return s.Store.AccountByID(p0, p1)
}

func (s *Store) AppByID(p0 context.Context, p1 string) (state.App, error) {
	if o, ok := s.over.(interface {
		AppByID(context.Context, string) (state.App, error)
	}); ok {
		return o.AppByID(p0, p1)
	}
	return s.Store.AppByID(p0, p1)
}

func (s *Store) AppendEvent(p0 context.Context, p1 string, p2 string, p3 *string, p4 []byte) error {
	if o, ok := s.over.(interface {
		AppendEvent(context.Context, string, string, *string, []byte) error
	}); ok {
		return o.AppendEvent(p0, p1, p2, p3, p4)
	}
	return s.Store.AppendEvent(p0, p1, p2, p3, p4)
}

func (s *Store) AppendEventWithTrace(p0 context.Context, p1 string, p2 string, p3 *string, p4 []byte, p5 *string) error {
	if o, ok := s.over.(interface {
		AppendEventWithTrace(context.Context, string, string, *string, []byte, *string) error
	}); ok {
		return o.AppendEventWithTrace(p0, p1, p2, p3, p4, p5)
	}
	return s.Store.AppendEventWithTrace(p0, p1, p2, p3, p4, p5)
}

func (s *Store) AppendUploadBytes(p0 context.Context, p1 sqlc.AppendUploadBytesParams) (sqlc.AppendUploadBytesRow, error) {
	if o, ok := s.over.(interface {
		AppendUploadBytes(context.Context, sqlc.AppendUploadBytesParams) (sqlc.AppendUploadBytesRow, error)
	}); ok {
		return o.AppendUploadBytes(p0, p1)
	}
	return s.Store.AppendUploadBytes(p0, p1)
}

func (s *Store) ClaimInvocation(p0 context.Context, p1 string, p2 string, p3 int) (state.Invocation, error) {
	if o, ok := s.over.(interface {
		ClaimInvocation(context.Context, string, string, int) (state.Invocation, error)
	}); ok {
		return o.ClaimInvocation(p0, p1, p2, p3)
	}
	return s.Store.ClaimInvocation(p0, p1, p2, p3)
}

func (s *Store) ClaimInvocationWithCap(p0 context.Context, p1 string, p2 string, p3 int, p4 int) (state.Invocation, error) {
	if o, ok := s.over.(interface {
		ClaimInvocationWithCap(context.Context, string, string, int, int) (state.Invocation, error)
	}); ok {
		return o.ClaimInvocationWithCap(p0, p1, p2, p3, p4)
	}
	return s.Store.ClaimInvocationWithCap(p0, p1, p2, p3, p4)
}

func (s *Store) ConsumerKeyByAppAndPrefix(p0 context.Context, p1 string, p2 string, p3 string) (state.ConsumerKey, error) {
	if o, ok := s.over.(interface {
		ConsumerKeyByAppAndPrefix(context.Context, string, string, string) (state.ConsumerKey, error)
	}); ok {
		return o.ConsumerKeyByAppAndPrefix(p0, p1, p2, p3)
	}
	return s.Store.ConsumerKeyByAppAndPrefix(p0, p1, p2, p3)
}

func (s *Store) DeploymentByID(p0 context.Context, p1 string) (state.Deployment, error) {
	if o, ok := s.over.(interface {
		DeploymentByID(context.Context, string) (state.Deployment, error)
	}); ok {
		return o.DeploymentByID(p0, p1)
	}
	return s.Store.DeploymentByID(p0, p1)
}

func (s *Store) EnqueueInvocation(p0 context.Context, p1 state.Invocation) (state.Invocation, error) {
	if o, ok := s.over.(interface {
		EnqueueInvocation(context.Context, state.Invocation) (state.Invocation, error)
	}); ok {
		return o.EnqueueInvocation(p0, p1)
	}
	return s.Store.EnqueueInvocation(p0, p1)
}

func (s *Store) GetAPIConsumerByID(p0 context.Context, p1 string, p2 string) (state.APIConsumer, error) {
	if o, ok := s.over.(interface {
		GetAPIConsumerByID(context.Context, string, string) (state.APIConsumer, error)
	}); ok {
		return o.GetAPIConsumerByID(p0, p1, p2)
	}
	return s.Store.GetAPIConsumerByID(p0, p1, p2)
}

func (s *Store) GetAccountOverageCapCents(p0 context.Context, p1 string) (int64, bool, error) {
	if o, ok := s.over.(interface {
		GetAccountOverageCapCents(context.Context, string) (int64, bool, error)
	}); ok {
		return o.GetAccountOverageCapCents(p0, p1)
	}
	return s.Store.GetAccountOverageCapCents(p0, p1)
}

func (s *Store) GetUploadSession(p0 context.Context, p1 string) (sqlc.UploadSession, error) {
	if o, ok := s.over.(interface {
		GetUploadSession(context.Context, string) (sqlc.UploadSession, error)
	}); ok {
		return o.GetUploadSession(p0, p1)
	}
	return s.Store.GetUploadSession(p0, p1)
}

func (s *Store) InsertOperatorIntent(p0 context.Context, p1 state.OperatorIntentKind, p2 string, p3 *string, p4 string, p5 string, p6 json.RawMessage, p7 *string) (string, error) {
	if o, ok := s.over.(interface {
		InsertOperatorIntent(context.Context, state.OperatorIntentKind, string, *string, string, string, json.RawMessage, *string) (string, error)
	}); ok {
		return o.InsertOperatorIntent(p0, p1, p2, p3, p4, p5, p6, p7)
	}
	return s.Store.InsertOperatorIntent(p0, p1, p2, p3, p4, p5, p6, p7)
}

func (s *Store) InstanceByID(p0 context.Context, p1 string) (state.Instance, error) {
	if o, ok := s.over.(interface {
		InstanceByID(context.Context, string) (state.Instance, error)
	}); ok {
		return o.InstanceByID(p0, p1)
	}
	return s.Store.InstanceByID(p0, p1)
}

func (s *Store) JobTaskClaimBatch(p0 context.Context, p1 int) ([]state.JobTask, error) {
	if o, ok := s.over.(interface {
		JobTaskClaimBatch(context.Context, int) ([]state.JobTask, error)
	}); ok {
		return o.JobTaskClaimBatch(p0, p1)
	}
	return s.Store.JobTaskClaimBatch(p0, p1)
}

func (s *Store) JobTaskMarkClaimed(p0 context.Context, p1 string, p2 int, p3 string, p4 string, p5 time.Time, p6 string) error {
	if o, ok := s.over.(interface {
		JobTaskMarkClaimed(context.Context, string, int, string, string, time.Time, string) error
	}); ok {
		return o.JobTaskMarkClaimed(p0, p1, p2, p3, p4, p5, p6)
	}
	return s.Store.JobTaskMarkClaimed(p0, p1, p2, p3, p4, p5, p6)
}

func (s *Store) ListAppSecretsInScope(p0 context.Context, p1 string, p2 string, p3 string) ([]state.AppSecret, error) {
	if o, ok := s.over.(interface {
		ListAppSecretsInScope(context.Context, string, string, string) ([]state.AppSecret, error)
	}); ok {
		return o.ListAppSecretsInScope(p0, p1, p2, p3)
	}
	return s.Store.ListAppSecretsInScope(p0, p1, p2, p3)
}

func (s *Store) ListApps(p0 context.Context, p1 string) ([]state.App, error) {
	if o, ok := s.over.(interface {
		ListApps(context.Context, string) ([]state.App, error)
	}); ok {
		return o.ListApps(p0, p1)
	}
	return s.Store.ListApps(p0, p1)
}

func (s *Store) ListDeploymentsForApp(p0 context.Context, p1 string, p2 int, p3 int) ([]state.Deployment, error) {
	if o, ok := s.over.(interface {
		ListDeploymentsForApp(context.Context, string, int, int) ([]state.Deployment, error)
	}); ok {
		return o.ListDeploymentsForApp(p0, p1, p2, p3)
	}
	return s.Store.ListDeploymentsForApp(p0, p1, p2, p3)
}

func (s *Store) ListInstancesForAccount(p0 context.Context, p1 string) ([]state.Instance, error) {
	if o, ok := s.over.(interface {
		ListInstancesForAccount(context.Context, string) ([]state.Instance, error)
	}); ok {
		return o.ListInstancesForAccount(p0, p1)
	}
	return s.Store.ListInstancesForAccount(p0, p1)
}

func (s *Store) ListMirrorRules(p0 context.Context, p1 string) ([]state.MirrorRule, error) {
	if o, ok := s.over.(interface {
		ListMirrorRules(context.Context, string) ([]state.MirrorRule, error)
	}); ok {
		return o.ListMirrorRules(p0, p1)
	}
	return s.Store.ListMirrorRules(p0, p1)
}

func (s *Store) ListOrphanedAppsPage(p0 context.Context, p1 int, p2 int, p3 string, p4 string) ([]state.OrphanedAppCandidate, error) {
	if o, ok := s.over.(interface {
		ListOrphanedAppsPage(context.Context, int, int, string, string) ([]state.OrphanedAppCandidate, error)
	}); ok {
		return o.ListOrphanedAppsPage(p0, p1, p2, p3, p4)
	}
	return s.Store.ListOrphanedAppsPage(p0, p1, p2, p3, p4)
}

func (s *Store) ListPublicStatusEvents(p0 context.Context, p1 state.StatusEventListOptions) ([]state.StatusIncident, error) {
	if o, ok := s.over.(interface {
		ListPublicStatusEvents(context.Context, state.StatusEventListOptions) ([]state.StatusIncident, error)
	}); ok {
		return o.ListPublicStatusEvents(p0, p1)
	}
	return s.Store.ListPublicStatusEvents(p0, p1)
}

func (s *Store) ListRequestTelemetryByPlatformTenant(p0 context.Context, p1 sqlc.ListRequestTelemetryByPlatformTenantParams) ([]sqlc.ListRequestTelemetryByPlatformTenantRow, error) {
	if o, ok := s.over.(interface {
		ListRequestTelemetryByPlatformTenant(context.Context, sqlc.ListRequestTelemetryByPlatformTenantParams) ([]sqlc.ListRequestTelemetryByPlatformTenantRow, error)
	}); ok {
		return o.ListRequestTelemetryByPlatformTenant(p0, p1)
	}
	return s.Store.ListRequestTelemetryByPlatformTenant(p0, p1)
}

func (s *Store) MarkSnapshotStale(p0 context.Context, p1 string) error {
	if o, ok := s.over.(interface {
		MarkSnapshotStale(context.Context, string) error
	}); ok {
		return o.MarkSnapshotStale(p0, p1)
	}
	return s.Store.MarkSnapshotStale(p0, p1)
}

func (s *Store) Ping(p0 context.Context) error {
	if o, ok := s.over.(interface{ Ping(context.Context) error }); ok {
		return o.Ping(p0)
	}
	return s.Store.Ping(p0)
}

func (s *Store) QueueStateInScope(p0 context.Context, p1 string, p2 string) (state.QueueStats, error) {
	if o, ok := s.over.(interface {
		QueueStateInScope(context.Context, string, string) (state.QueueStats, error)
	}); ok {
		return o.QueueStateInScope(p0, p1, p2)
	}
	return s.Store.QueueStateInScope(p0, p1, p2)
}

func (s *Store) ReapExpiredUploadSessions(p0 context.Context) ([]sqlc.ReapExpiredUploadSessionsRow, error) {
	if o, ok := s.over.(interface {
		ReapExpiredUploadSessions(context.Context) ([]sqlc.ReapExpiredUploadSessionsRow, error)
	}); ok {
		return o.ReapExpiredUploadSessions(p0)
	}
	return s.Store.ReapExpiredUploadSessions(p0)
}

func (s *Store) RecordAppSecretDelivery(p0 context.Context, p1 state.AppSecretDeliveryResult) (int, error) {
	if o, ok := s.over.(interface {
		RecordAppSecretDelivery(context.Context, state.AppSecretDeliveryResult) (int, error)
	}); ok {
		return o.RecordAppSecretDelivery(p0, p1)
	}
	return s.Store.RecordAppSecretDelivery(p0, p1)
}

func (s *Store) RuntimeAppValuesForDeployment(p0 context.Context, p1 string, p2 string, p3 string) (state.RuntimeAppValuesSnapshot, error) {
	if o, ok := s.over.(interface {
		RuntimeAppValuesForDeployment(context.Context, string, string, string) (state.RuntimeAppValuesSnapshot, error)
	}); ok {
		return o.RuntimeAppValuesForDeployment(p0, p1, p2, p3)
	}
	return s.Store.RuntimeAppValuesForDeployment(p0, p1, p2, p3)
}

func (s *Store) TouchConsumerKeyLastUsed(p0 context.Context, p1 string) error {
	if o, ok := s.over.(interface {
		TouchConsumerKeyLastUsed(context.Context, string) error
	}); ok {
		return o.TouchConsumerKeyLastUsed(p0, p1)
	}
	return s.Store.TouchConsumerKeyLastUsed(p0, p1)
}

func (s *Store) UpdateInstanceStateToTerminal(p0 context.Context, p1 string, p2 string, p3 time.Time) error {
	if o, ok := s.over.(interface {
		UpdateInstanceStateToTerminal(context.Context, string, string, time.Time) error
	}); ok {
		return o.UpdateInstanceStateToTerminal(p0, p1, p2, p3)
	}
	return s.Store.UpdateInstanceStateToTerminal(p0, p1, p2, p3)
}

func (s *Store) UpdateProjectEnvironmentPromotionVerification(p0 context.Context, p1 string, p2 string, p3 string, p4 string, p5 *time.Time, p6 *time.Time) (state.ProjectEnvironmentPromotion, error) {
	if o, ok := s.over.(interface {
		UpdateProjectEnvironmentPromotionVerification(context.Context, string, string, string, string, *time.Time, *time.Time) (state.ProjectEnvironmentPromotion, error)
	}); ok {
		return o.UpdateProjectEnvironmentPromotionVerification(p0, p1, p2, p3, p4, p5, p6)
	}
	return s.Store.UpdateProjectEnvironmentPromotionVerification(p0, p1, p2, p3, p4, p5, p6)
}

func (s *Store) UsageByAccount(p0 context.Context, p1 string, p2 time.Time) ([]state.Usage, error) {
	if o, ok := s.over.(interface {
		UsageByAccount(context.Context, string, time.Time) ([]state.Usage, error)
	}); ok {
		return o.UsageByAccount(p0, p1, p2)
	}
	return s.Store.UsageByAccount(p0, p1, p2)
}

func (s *Store) UsageSLOForAccount(p0 context.Context, p1 string, p2 time.Time, p3 time.Time) (float64, float64, error) {
	if o, ok := s.over.(interface {
		UsageSLOForAccount(context.Context, string, time.Time, time.Time) (float64, float64, error)
	}); ok {
		return o.UsageSLOForAccount(p0, p1, p2, p3)
	}
	return s.Store.UsageSLOForAccount(p0, p1, p2, p3)
}

// Runtime policy capabilities used by external apid tests.
func (s *Store) LatestAppControlPlaneChangeID(ctx context.Context, appID string) (int64, error) {
	if override, ok := s.over.(interface {
		LatestAppControlPlaneChangeID(ctx context.Context, appID string) (int64, error)
	}); ok {
		return override.LatestAppControlPlaneChangeID(ctx, appID)
	}
	if source, ok := s.Store.(interface {
		LatestAppControlPlaneChangeID(ctx context.Context, appID string) (int64, error)
	}); ok {
		return source.LatestAppControlPlaneChangeID(ctx, appID)
	}
	return 0, state.ErrNotFound
}
func (s *Store) LatestAppRequestPolicyRevision(ctx context.Context, appID string) (int64, error) {
	if override, ok := s.over.(interface {
		LatestAppRequestPolicyRevision(ctx context.Context, appID string) (int64, error)
	}); ok {
		return override.LatestAppRequestPolicyRevision(ctx, appID)
	}
	if source, ok := s.Store.(interface {
		LatestAppRequestPolicyRevision(ctx context.Context, appID string) (int64, error)
	}); ok {
		return source.LatestAppRequestPolicyRevision(ctx, appID)
	}
	return 0, state.ErrNotFound
}
func (s *Store) LatestAppEdgeRuleChangeID(ctx context.Context, appID string) (int64, error) {
	if override, ok := s.over.(interface {
		LatestAppEdgeRuleChangeID(ctx context.Context, appID string) (int64, error)
	}); ok {
		return override.LatestAppEdgeRuleChangeID(ctx, appID)
	}
	if source, ok := s.Store.(interface {
		LatestAppEdgeRuleChangeID(ctx context.Context, appID string) (int64, error)
	}); ok {
		return source.LatestAppEdgeRuleChangeID(ctx, appID)
	}
	return 0, state.ErrNotFound
}
func (s *Store) LatestAccountCorsPresetChangeID(ctx context.Context, accountID string) (int64, error) {
	if override, ok := s.over.(interface {
		LatestAccountCorsPresetChangeID(ctx context.Context, accountID string) (int64, error)
	}); ok {
		return override.LatestAccountCorsPresetChangeID(ctx, accountID)
	}
	if source, ok := s.Store.(interface {
		LatestAccountCorsPresetChangeID(ctx context.Context, accountID string) (int64, error)
	}); ok {
		return source.LatestAccountCorsPresetChangeID(ctx, accountID)
	}
	return 0, state.ErrNotFound
}
func (s *Store) LatestAppResponseCachePurgeID(ctx context.Context, appID string) (int64, error) {
	if override, ok := s.over.(interface {
		LatestAppResponseCachePurgeID(ctx context.Context, appID string) (int64, error)
	}); ok {
		return override.LatestAppResponseCachePurgeID(ctx, appID)
	}
	if source, ok := s.Store.(interface {
		LatestAppResponseCachePurgeID(ctx context.Context, appID string) (int64, error)
	}); ok {
		return source.LatestAppResponseCachePurgeID(ctx, appID)
	}
	return 0, state.ErrNotFound
}
func (s *Store) LatestAppEgressPolicyRevision(ctx context.Context, appID string) (int64, error) {
	if override, ok := s.over.(interface {
		LatestAppEgressPolicyRevision(ctx context.Context, appID string) (int64, error)
	}); ok {
		return override.LatestAppEgressPolicyRevision(ctx, appID)
	}
	if source, ok := s.Store.(interface {
		LatestAppEgressPolicyRevision(ctx context.Context, appID string) (int64, error)
	}); ok {
		return source.LatestAppEgressPolicyRevision(ctx, appID)
	}
	return 0, state.ErrNotFound
}
func (s *Store) LatestAppCPUPolicyRevision(ctx context.Context, appID string) (int64, error) {
	if override, ok := s.over.(interface {
		LatestAppCPUPolicyRevision(ctx context.Context, appID string) (int64, error)
	}); ok {
		return override.LatestAppCPUPolicyRevision(ctx, appID)
	}
	if source, ok := s.Store.(interface {
		LatestAppCPUPolicyRevision(ctx context.Context, appID string) (int64, error)
	}); ok {
		return source.LatestAppCPUPolicyRevision(ctx, appID)
	}
	return 0, state.ErrNotFound
}
func (s *Store) ListServingGatewayControlPlaneStates(ctx context.Context) ([]state.ServingGatewayControlPlaneState, error) {
	if override, ok := s.over.(interface {
		ListServingGatewayControlPlaneStates(ctx context.Context) ([]state.ServingGatewayControlPlaneState, error)
	}); ok {
		return override.ListServingGatewayControlPlaneStates(ctx)
	}
	if source, ok := s.Store.(interface {
		ListServingGatewayControlPlaneStates(ctx context.Context) ([]state.ServingGatewayControlPlaneState, error)
	}); ok {
		return source.ListServingGatewayControlPlaneStates(ctx)
	}
	return nil, state.ErrNotFound
}
func (s *Store) ListServingAppEgressPolicyNodeStates(ctx context.Context, appID string) ([]state.AppEgressPolicyNodeState, error) {
	if override, ok := s.over.(interface {
		ListServingAppEgressPolicyNodeStates(ctx context.Context, appID string) ([]state.AppEgressPolicyNodeState, error)
	}); ok {
		return override.ListServingAppEgressPolicyNodeStates(ctx, appID)
	}
	if source, ok := s.Store.(interface {
		ListServingAppEgressPolicyNodeStates(ctx context.Context, appID string) ([]state.AppEgressPolicyNodeState, error)
	}); ok {
		return source.ListServingAppEgressPolicyNodeStates(ctx, appID)
	}
	return nil, state.ErrNotFound
}
func (s *Store) ListServingAppCPUPolicyNodeStates(ctx context.Context, appID string) ([]state.AppCPUPolicyNodeState, error) {
	if override, ok := s.over.(interface {
		ListServingAppCPUPolicyNodeStates(ctx context.Context, appID string) ([]state.AppCPUPolicyNodeState, error)
	}); ok {
		return override.ListServingAppCPUPolicyNodeStates(ctx, appID)
	}
	if source, ok := s.Store.(interface {
		ListServingAppCPUPolicyNodeStates(ctx context.Context, appID string) ([]state.AppCPUPolicyNodeState, error)
	}); ok {
		return source.ListServingAppCPUPolicyNodeStates(ctx, appID)
	}
	return nil, state.ErrNotFound
}
func (s *Store) GetAppScalingPolicyStatus(ctx context.Context, appID string) (state.AppScalingPolicyStatus, error) {
	if override, ok := s.over.(interface {
		GetAppScalingPolicyStatus(ctx context.Context, appID string) (state.AppScalingPolicyStatus, error)
	}); ok {
		return override.GetAppScalingPolicyStatus(ctx, appID)
	}
	if source, ok := s.Store.(interface {
		GetAppScalingPolicyStatus(ctx context.Context, appID string) (state.AppScalingPolicyStatus, error)
	}); ok {
		return source.GetAppScalingPolicyStatus(ctx, appID)
	}
	return state.AppScalingPolicyStatus{}, state.ErrNotFound
}
func (s *Store) LatestEdgeRuleChangeID(ctx context.Context) (int64, error) {
	if override, ok := s.over.(interface {
		LatestEdgeRuleChangeID(ctx context.Context) (int64, error)
	}); ok {
		return override.LatestEdgeRuleChangeID(ctx)
	}
	if source, ok := s.Store.(interface {
		LatestEdgeRuleChangeID(ctx context.Context) (int64, error)
	}); ok {
		return source.LatestEdgeRuleChangeID(ctx)
	}
	return 0, state.ErrNotFound
}
func (s *Store) LatestAccountEdgeRulePolicyPosition(ctx context.Context, accountID string) (state.EdgeRulePolicyPosition, error) {
	if override, ok := s.over.(interface {
		LatestAccountEdgeRulePolicyPosition(ctx context.Context, accountID string) (state.EdgeRulePolicyPosition, error)
	}); ok {
		return override.LatestAccountEdgeRulePolicyPosition(ctx, accountID)
	}
	if source, ok := s.Store.(interface {
		LatestAccountEdgeRulePolicyPosition(ctx context.Context, accountID string) (state.EdgeRulePolicyPosition, error)
	}); ok {
		return source.LatestAccountEdgeRulePolicyPosition(ctx, accountID)
	}
	return state.EdgeRulePolicyPosition{}, state.ErrNotFound
}
func (s *Store) MCPPolicyConvergenceSnapshot(ctx context.Context, accountID, appID string) (api.MCPPolicyConvergenceSnapshot, error) {
	if override, ok := s.over.(interface {
		MCPPolicyConvergenceSnapshot(ctx context.Context, accountID, appID string) (api.MCPPolicyConvergenceSnapshot, error)
	}); ok {
		return override.MCPPolicyConvergenceSnapshot(ctx, accountID, appID)
	}
	if source, ok := s.Store.(interface {
		MCPPolicyConvergenceSnapshot(ctx context.Context, accountID, appID string) (api.MCPPolicyConvergenceSnapshot, error)
	}); ok {
		return source.MCPPolicyConvergenceSnapshot(ctx, accountID, appID)
	}
	return api.MCPPolicyConvergenceSnapshot{}, state.ErrNotFound
}

func (s *Mem) ScenarioWebhookDeliveryBlocked(ctx context.Context, appID, webhookID string) (bool, error) {
	if f, ok := s.over.(interface {
		ScenarioWebhookDeliveryBlocked(context.Context, string, string) (bool, error)
	}); ok {
		return f.ScenarioWebhookDeliveryBlocked(ctx, appID, webhookID)
	}
	return s.MemStore.ScenarioWebhookDeliveryBlocked(ctx, appID, webhookID)
}

func (s *Pg) ScenarioWebhookDeliveryBlocked(ctx context.Context, appID, webhookID string) (bool, error) {
	if f, ok := s.over.(interface {
		ScenarioWebhookDeliveryBlocked(context.Context, string, string) (bool, error)
	}); ok {
		return f.ScenarioWebhookDeliveryBlocked(ctx, appID, webhookID)
	}
	return s.PgStore.ScenarioWebhookDeliveryBlocked(ctx, appID, webhookID)
}

func (s *Store) ScenarioWebhookDeliveryBlocked(ctx context.Context, appID, webhookID string) (bool, error) {
	if f, ok := s.over.(interface {
		ScenarioWebhookDeliveryBlocked(context.Context, string, string) (bool, error)
	}); ok {
		return f.ScenarioWebhookDeliveryBlocked(ctx, appID, webhookID)
	}
	return s.Store.ScenarioWebhookDeliveryBlocked(ctx, appID, webhookID)
}

func (s *Mem) CompleteNodeLifecycle(ctx context.Context, in state.NodeLifecycleCompletion) (state.NodeLifecycleCompletionResult, error) {
	if override, ok := s.over.(state.NodeLifecycleCompletionStore); ok {
		return override.CompleteNodeLifecycle(ctx, in)
	}
	return s.MemStore.CompleteNodeLifecycle(ctx, in)
}

func (s *Mem) CreateInstanceWithMode(ctx context.Context, appID, deploymentID, current string, ramMB int, nodeID, wakeID, mode string) (state.Instance, error) {
	if override, ok := s.over.(interface {
		CreateInstanceWithMode(context.Context, string, string, string, int, string, string, string) (state.Instance, error)
	}); ok {
		return override.CreateInstanceWithMode(ctx, appID, deploymentID, current, ramMB, nodeID, wakeID, mode)
	}
	return s.MemStore.CreateInstanceWithMode(ctx, appID, deploymentID, current, ramMB, nodeID, wakeID, mode)
}

func (s *Mem) CreateAndClaimJobInstance(ctx context.Context, id, job, run string, index int, current string, ramMB int, node, wake, token string, expires time.Time, owner string) (state.Instance, error) {
	if override, ok := s.over.(interface {
		CreateAndClaimJobInstance(context.Context, string, string, string, int, string, int, string, string, string, time.Time, string) (state.Instance, error)
	}); ok {
		return override.CreateAndClaimJobInstance(ctx, id, job, run, index, current, ramMB, node, wake, token, expires, owner)
	}
	return s.MemStore.CreateAndClaimJobInstance(ctx, id, job, run, index, current, ramMB, node, wake, token, expires, owner)
}

func (s *Mem) DeliverAuditEvent(ctx context.Context, id int64) (bool, error) {
	if override, ok := s.over.(interface {
		DeliverAuditEvent(context.Context, int64) (bool, error)
	}); ok {
		return override.DeliverAuditEvent(ctx, id)
	}
	return s.MemStore.DeliverAuditEvent(ctx, id)
}

func (s *Mem) ExecuteOperatorNode(ctx context.Context, id, token string) (state.OperatorNodeResult, error) {
	if override, ok := s.over.(state.OperatorNodeStore); ok {
		return override.ExecuteOperatorNode(ctx, id, token)
	}
	return s.MemStore.ExecuteOperatorNode(ctx, id, token)
}

func (s *Mem) ExecuteOperatorColdBoot(ctx context.Context, id, token string) (state.OperatorColdBootResult, error) {
	if override, ok := s.over.(state.OperatorColdBootStore); ok {
		return override.ExecuteOperatorColdBoot(ctx, id, token)
	}
	return s.MemStore.ExecuteOperatorColdBoot(ctx, id, token)
}
func (s *Mem) BindInstanceSnapshotRecovery(ctx context.Context, id, wake, snapshot string) error {
	if override, ok := s.over.(interface {
		BindInstanceSnapshotRecovery(context.Context, string, string, string) error
	}); ok {
		return override.BindInstanceSnapshotRecovery(ctx, id, wake, snapshot)
	}
	return s.MemStore.BindInstanceSnapshotRecovery(ctx, id, wake, snapshot)
}

func (s *Mem) SubmitOperatorRecovery(ctx context.Context, in state.OperatorRecoverySubmission) (state.OperatorRecoveryReceipt, error) {
	if override, ok := s.over.(state.OperatorRecoverySubmissionStore); ok {
		return override.SubmitOperatorRecovery(ctx, in)
	}
	return s.MemStore.SubmitOperatorRecovery(ctx, in)
}

func (s *Pg) SubmitOperatorRecovery(ctx context.Context, in state.OperatorRecoverySubmission) (state.OperatorRecoveryReceipt, error) {
	if override, ok := s.over.(state.OperatorRecoverySubmissionStore); ok {
		return override.SubmitOperatorRecovery(ctx, in)
	}
	return s.PgStore.SubmitOperatorRecovery(ctx, in)
}

func (s *Store) SubmitOperatorRecovery(ctx context.Context, in state.OperatorRecoverySubmission) (state.OperatorRecoveryReceipt, error) {
	if override, ok := s.over.(state.OperatorRecoverySubmissionStore); ok {
		return override.SubmitOperatorRecovery(ctx, in)
	}
	if store, ok := s.Store.(state.OperatorRecoverySubmissionStore); ok {
		return store.SubmitOperatorRecovery(ctx, in)
	}
	return state.OperatorRecoveryReceipt{}, state.ErrInvalidArgument
}

func (s *Mem) CompleteOperatorPark(ctx context.Context, completion state.OperatorParkCompletion) (state.OperatorParkResult, error) {
	if override, ok := s.over.(interface {
		CompleteOperatorPark(context.Context, state.OperatorParkCompletion) (state.OperatorParkResult, error)
	}); ok {
		return override.CompleteOperatorPark(ctx, completion)
	}
	return s.MemStore.CompleteOperatorPark(ctx, completion)
}

func (s *Mem) ClaimNextQueuedBuild(ctx context.Context) (state.Build, error) {
	if over, ok := s.over.(interface {
		ClaimNextQueuedBuild(context.Context) (state.Build, error)
	}); ok {
		return over.ClaimNextQueuedBuild(ctx)
	}
	return s.MemStore.ClaimNextQueuedBuild(ctx)
}

func (s *Mem) ClaimQueuedBuild(ctx context.Context, id string) (state.Build, error) {
	if over, ok := s.over.(interface {
		ClaimQueuedBuild(context.Context, string) (state.Build, error)
	}); ok {
		return over.ClaimQueuedBuild(ctx, id)
	}
	return s.MemStore.ClaimQueuedBuild(ctx, id)
}

func (s *Mem) ListBuildsAwaitingImage(ctx context.Context, node string, limit int) ([]state.BuildImageWork, error) {
	if o, ok := s.over.(interface {
		ListBuildsAwaitingImage(context.Context, string, int) ([]state.BuildImageWork, error)
	}); ok {
		return o.ListBuildsAwaitingImage(ctx, node, limit)
	}
	return s.MemStore.ListBuildsAwaitingImage(ctx, node, limit)
}

func (s *Mem) ListDeploymentsForOperator(ctx context.Context, filter state.OperatorDeploymentFilter) ([]state.Deployment, error) {
	if f, ok := s.over.(interface {
		ListDeploymentsForOperator(context.Context, state.OperatorDeploymentFilter) ([]state.Deployment, error)
	}); ok {
		return f.ListDeploymentsForOperator(ctx, filter)
	}
	return s.MemStore.ListDeploymentsForOperator(ctx, filter)
}

func (s *Mem) ListResumableImagePreparations(ctx context.Context, node string, limit int) ([]state.BuildImageWork, error) {
	if o, ok := s.over.(interface {
		ListResumableImagePreparations(context.Context, string, int) ([]state.BuildImageWork, error)
	}); ok {
		return o.ListResumableImagePreparations(ctx, node, limit)
	}
	return s.MemStore.ListResumableImagePreparations(ctx, node, limit)
}

func (s *Mem) PublishImagePreparationLayer(ctx context.Context, id, token, path, key string, bytes int64) error {
	if o, ok := s.over.(interface {
		PublishImagePreparationLayer(context.Context, string, string, string, string, int64) error
	}); ok {
		return o.PublishImagePreparationLayer(ctx, id, token, path, key, bytes)
	}
	return s.MemStore.PublishImagePreparationLayer(ctx, id, token, path, key, bytes)
}

func (s *Mem) RequeueBuildIfClaim(ctx context.Context, claim state.Build) error {
	if over, ok := s.over.(interface {
		RequeueBuildIfClaim(context.Context, state.Build) error
	}); ok {
		return over.RequeueBuildIfClaim(ctx, claim)
	}
	return s.MemStore.RequeueBuildIfClaim(ctx, claim)
}

func (s *Store) ListAllApps(ctx context.Context) ([]state.App, error) {
	if o, ok := s.over.(interface {
		ListAllApps(context.Context) ([]state.App, error)
	}); ok {
		return o.ListAllApps(ctx)
	}
	return s.Store.ListAllApps(ctx)
}

func (s *Store) ListAllInstances(ctx context.Context) ([]state.Instance, error) {
	if o, ok := s.over.(interface {
		ListAllInstances(context.Context) ([]state.Instance, error)
	}); ok {
		return o.ListAllInstances(ctx)
	}
	return s.Store.ListAllInstances(ctx)
}

func (s *Store) OperatorAlertCollectionHistory(ctx context.Context, f state.OperatorAlertCollectionHistoryFilter) (state.OperatorAlertCollectionHistorySnapshot, error) {
	if o, ok := s.over.(interface {
		OperatorAlertCollectionHistory(context.Context, state.OperatorAlertCollectionHistoryFilter) (state.OperatorAlertCollectionHistorySnapshot, error)
	}); ok {
		return o.OperatorAlertCollectionHistory(ctx, f)
	}
	return s.Store.OperatorAlertCollectionHistory(ctx, f)
}

func (s *Store) OperatorAlertCollectionStatus(ctx context.Context) (state.OperatorAlertCollection, error) {
	if o, ok := s.over.(interface {
		OperatorAlertCollectionStatus(context.Context) (state.OperatorAlertCollection, error)
	}); ok {
		return o.OperatorAlertCollectionStatus(ctx)
	}
	return s.Store.OperatorAlertCollectionStatus(ctx)
}

func (s *Store) OperatorAlertEpisode(ctx context.Context, id string) (state.OperatorAlertEpisode, state.OperatorAlertCollection, error) {
	if o, ok := s.over.(interface {
		OperatorAlertEpisode(context.Context, string) (state.OperatorAlertEpisode, state.OperatorAlertCollection, error)
	}); ok {
		return o.OperatorAlertEpisode(ctx, id)
	}
	return s.Store.OperatorAlertEpisode(ctx, id)
}

func (s *Store) OperatorAlertEpisodes(ctx context.Context, f state.OperatorAlertFilter) (state.OperatorAlertPage, error) {
	if o, ok := s.over.(interface {
		OperatorAlertEpisodes(context.Context, state.OperatorAlertFilter) (state.OperatorAlertPage, error)
	}); ok {
		return o.OperatorAlertEpisodes(ctx, f)
	}
	return s.Store.OperatorAlertEpisodes(ctx, f)
}

func (s *Store) OperatorAlertRecurrence(ctx context.Context, f state.OperatorAlertRecurrenceFilter) (state.OperatorAlertRecurrenceSnapshot, error) {
	if o, ok := s.over.(interface {
		OperatorAlertRecurrence(context.Context, state.OperatorAlertRecurrenceFilter) (state.OperatorAlertRecurrenceSnapshot, error)
	}); ok {
		return o.OperatorAlertRecurrence(ctx, f)
	}
	return s.Store.OperatorAlertRecurrence(ctx, f)
}

func (s *Store) OperatorAlertResponseTimes(ctx context.Context, f state.OperatorAlertResponseTimesFilter) (state.OperatorAlertResponseTimesSnapshot, error) {
	if o, ok := s.over.(interface {
		OperatorAlertResponseTimes(context.Context, state.OperatorAlertResponseTimesFilter) (state.OperatorAlertResponseTimesSnapshot, error)
	}); ok {
		return o.OperatorAlertResponseTimes(ctx, f)
	}
	return s.Store.OperatorAlertResponseTimes(ctx, f)
}

func (s *Store) OperatorAlertRetentionStatus(ctx context.Context, now time.Time) (state.OperatorAlertRetention, error) {
	if o, ok := s.over.(interface {
		OperatorAlertRetentionStatus(context.Context, time.Time) (state.OperatorAlertRetention, error)
	}); ok {
		return o.OperatorAlertRetentionStatus(ctx, now)
	}
	return s.Store.OperatorAlertRetentionStatus(ctx, now)
}

func (s *Store) OperatorAlertTriageHistory(ctx context.Context, f state.OperatorAlertTriageHistoryFilter) (state.OperatorAlertTriageHistorySnapshot, error) {
	if o, ok := s.over.(interface {
		OperatorAlertTriageHistory(context.Context, state.OperatorAlertTriageHistoryFilter) (state.OperatorAlertTriageHistorySnapshot, error)
	}); ok {
		return o.OperatorAlertTriageHistory(ctx, f)
	}
	return s.Store.OperatorAlertTriageHistory(ctx, f)
}

func (s *Store) OperatorAlertWorkload(ctx context.Context, f state.OperatorAlertWorkloadFilter) (state.OperatorAlertWorkloadSnapshot, error) {
	if o, ok := s.over.(interface {
		OperatorAlertWorkload(context.Context, state.OperatorAlertWorkloadFilter) (state.OperatorAlertWorkloadSnapshot, error)
	}); ok {
		return o.OperatorAlertWorkload(ctx, f)
	}
	return s.Store.OperatorAlertWorkload(ctx, f)
}

func (s *Store) OperatorAuditTraceCoverage(ctx context.Context, since time.Time) (float64, error) {
	if o, ok := s.over.(interface {
		OperatorAuditTraceCoverage(context.Context, time.Time) (float64, error)
	}); ok {
		return o.OperatorAuditTraceCoverage(ctx, since)
	}
	return s.Store.OperatorAuditTraceCoverage(ctx, since)
}

func (s *Store) OperatorCapacityProfiles(ctx context.Context) ([]api.ObsCapacityProfile, error) {
	if o, ok := s.over.(interface {
		OperatorCapacityProfiles(context.Context) ([]api.ObsCapacityProfile, error)
	}); ok {
		return o.OperatorCapacityProfiles(ctx)
	}
	return s.Store.OperatorCapacityProfiles(ctx)
}

func (s *Store) OperatorIncidentContext(ctx context.Context, f state.OperatorIncidentContextFilter) (api.ObsIncidentContextResponse, error) {
	if o, ok := s.over.(interface {
		OperatorIncidentContext(context.Context, state.OperatorIncidentContextFilter) (api.ObsIncidentContextResponse, error)
	}); ok {
		return o.OperatorIncidentContext(ctx, f)
	}
	return s.Store.OperatorIncidentContext(ctx, f)
}

func (s *Store) OperatorOverview(ctx context.Context, now time.Time) (api.ObsOverviewResponse, error) {
	if o, ok := s.over.(interface {
		OperatorOverview(context.Context, time.Time) (api.ObsOverviewResponse, error)
	}); ok {
		return o.OperatorOverview(ctx, now)
	}
	return s.Store.OperatorOverview(ctx, now)
}

func (s *Store) OperatorTenantApps(ctx context.Context, accountID string) ([]api.ObsTenantApp, error) {
	if o, ok := s.over.(interface {
		OperatorTenantApps(context.Context, string) ([]api.ObsTenantApp, error)
	}); ok {
		return o.OperatorTenantApps(ctx, accountID)
	}
	return s.Store.OperatorTenantApps(ctx, accountID)
}

func (s *Store) RecordOperatorAlertSnapshot(ctx context.Context, snap state.OperatorAlertSnapshot) error {
	if o, ok := s.over.(interface {
		RecordOperatorAlertSnapshot(context.Context, state.OperatorAlertSnapshot) error
	}); ok {
		return o.RecordOperatorAlertSnapshot(ctx, snap)
	}
	return s.Store.RecordOperatorAlertSnapshot(ctx, snap)
}

func (s *Mem) AppTaskAttachByTask(p0 context.Context, p1 string) (state.AppTaskAttach, error) {
	if o, ok := s.over.(interface {
		AppTaskAttachByTask(context.Context, string) (state.AppTaskAttach, error)
	}); ok {
		return o.AppTaskAttachByTask(p0, p1)
	}
	return s.MemStore.AppTaskAttachByTask(p0, p1)
}

func (s *Mem) ColdWakeRatePctSince(p0 context.Context, p1 string, p2 string, p3 time.Time) (float64, error) {
	if o, ok := s.over.(interface {
		ColdWakeRatePctSince(context.Context, string, string, time.Time) (float64, error)
	}); ok {
		return o.ColdWakeRatePctSince(p0, p1, p2, p3)
	}
	return s.MemStore.ColdWakeRatePctSince(p0, p1, p2, p3)
}

func (s *Mem) CountNewErrorFingerprintsSince(p0 context.Context, p1 string, p2 string, p3 time.Time) (int, error) {
	if o, ok := s.over.(interface {
		CountNewErrorFingerprintsSince(context.Context, string, string, time.Time) (int, error)
	}); ok {
		return o.CountNewErrorFingerprintsSince(p0, p1, p2, p3)
	}
	return s.MemStore.CountNewErrorFingerprintsSince(p0, p1, p2, p3)
}

func (s *Mem) DailyCostCents(p0 context.Context, p1 string, p2 string, p3 time.Time) (int64, error) {
	if o, ok := s.over.(interface {
		DailyCostCents(context.Context, string, string, time.Time) (int64, error)
	}); ok {
		return o.DailyCostCents(p0, p1, p2, p3)
	}
	return s.MemStore.DailyCostCents(p0, p1, p2, p3)
}

func (s *Mem) DeleteSnapshotsByID(p0 context.Context, p1 []string) (int64, error) {
	if o, ok := s.over.(interface {
		DeleteSnapshotsByID(context.Context, []string) (int64, error)
	}); ok {
		return o.DeleteSnapshotsByID(p0, p1)
	}
	return s.MemStore.DeleteSnapshotsByID(p0, p1)
}

func (s *Mem) DeploymentRuntimeUpgradeTarget(p0 context.Context, p1 string) (state.RuntimeRelease, error) {
	if o, ok := s.over.(interface {
		DeploymentRuntimeUpgradeTarget(context.Context, string) (state.RuntimeRelease, error)
	}); ok {
		return o.DeploymentRuntimeUpgradeTarget(p0, p1)
	}
	return s.MemStore.DeploymentRuntimeUpgradeTarget(p0, p1)
}

func (s *Mem) FailDeploymentWithHostingReceipt(p0 context.Context, p1 string, p2 []byte, p3 string, p4 string) (bool, error) {
	if o, ok := s.over.(interface {
		FailDeploymentWithHostingReceipt(context.Context, string, []byte, string, string) (bool, error)
	}); ok {
		return o.FailDeploymentWithHostingReceipt(p0, p1, p2, p3, p4)
	}
	return s.MemStore.FailDeploymentWithHostingReceipt(p0, p1, p2, p3, p4)
}

func (s *Mem) GetApplicationStandardEnrollment(p0 context.Context, p1 string, p2 string) (state.ApplicationStandardEnrollment, error) {
	if o, ok := s.over.(interface {
		GetApplicationStandardEnrollment(context.Context, string, string) (state.ApplicationStandardEnrollment, error)
	}); ok {
		return o.GetApplicationStandardEnrollment(p0, p1, p2)
	}
	return s.MemStore.GetApplicationStandardEnrollment(p0, p1, p2)
}

func (s *Mem) GetApplicationStandardSnapshotCapture(p0 context.Context, p1 string, p2 string, p3 string, p4 string) (state.ApplicationStandardSnapshotCaptureRecord, error) {
	if o, ok := s.over.(interface {
		GetApplicationStandardSnapshotCapture(context.Context, string, string, string, string) (state.ApplicationStandardSnapshotCaptureRecord, error)
	}); ok {
		return o.GetApplicationStandardSnapshotCapture(p0, p1, p2, p3, p4)
	}
	return s.MemStore.GetApplicationStandardSnapshotCapture(p0, p1, p2, p3, p4)
}

func (s *Mem) GetFreshBaseImageScan(p0 context.Context, p1 string, p2 string) (state.BaseImageScan, error) {
	if o, ok := s.over.(interface {
		GetFreshBaseImageScan(context.Context, string, string) (state.BaseImageScan, error)
	}); ok {
		return o.GetFreshBaseImageScan(p0, p1, p2)
	}
	return s.MemStore.GetFreshBaseImageScan(p0, p1, p2)
}

func (s *Mem) GetFreshBuildExportPublication(p0 context.Context, p1 string, p2 string, p3 string, p4 string) (state.BuildExportPublication, error) {
	if o, ok := s.over.(interface {
		GetFreshBuildExportPublication(context.Context, string, string, string, string) (state.BuildExportPublication, error)
	}); ok {
		return o.GetFreshBuildExportPublication(p0, p1, p2, p3, p4)
	}
	return s.MemStore.GetFreshBuildExportPublication(p0, p1, p2, p3, p4)
}

func (s *Mem) GetFreshDeploymentRuntimeProducerInputs(p0 context.Context, p1 string, p2 string, p3 string) (state.DeploymentRuntimeProducerInputs, error) {
	if o, ok := s.over.(interface {
		GetFreshDeploymentRuntimeProducerInputs(context.Context, string, string, string) (state.DeploymentRuntimeProducerInputs, error)
	}); ok {
		return o.GetFreshDeploymentRuntimeProducerInputs(p0, p1, p2, p3)
	}
	return s.MemStore.GetFreshDeploymentRuntimeProducerInputs(p0, p1, p2, p3)
}

func (s *Mem) GetFreshDeploymentRuntimeScan(p0 context.Context, p1 string, p2 string, p3 string) (state.DeploymentRuntimeScanEvidence, error) {
	if o, ok := s.over.(interface {
		GetFreshDeploymentRuntimeScan(context.Context, string, string, string) (state.DeploymentRuntimeScanEvidence, error)
	}); ok {
		return o.GetFreshDeploymentRuntimeScan(p0, p1, p2, p3)
	}
	return s.MemStore.GetFreshDeploymentRuntimeScan(p0, p1, p2, p3)
}

func (s *Mem) GetInstanceApplicationStandardAdmission(p0 context.Context, p1 string) (state.InstanceApplicationStandardAdmission, error) {
	if o, ok := s.over.(interface {
		GetInstanceApplicationStandardAdmission(context.Context, string) (state.InstanceApplicationStandardAdmission, error)
	}); ok {
		return o.GetInstanceApplicationStandardAdmission(p0, p1)
	}
	return s.MemStore.GetInstanceApplicationStandardAdmission(p0, p1)
}

func (s *Mem) GetInstanceApplicationStandardRuntimeReceipt(p0 context.Context, p1 string) (runtimeadmission.Receipt, error) {
	if o, ok := s.over.(interface {
		GetInstanceApplicationStandardRuntimeReceipt(context.Context, string) (runtimeadmission.Receipt, error)
	}); ok {
		return o.GetInstanceApplicationStandardRuntimeReceipt(p0, p1)
	}
	return s.MemStore.GetInstanceApplicationStandardRuntimeReceipt(p0, p1)
}

func (s *Mem) IssueApplicationStandardSnapshotCapture(p0 context.Context, p1 string, p2 state.ApplicationStandardSnapshotCaptureRequest) (runtimeadmission.SnapshotGrant, error) {
	if o, ok := s.over.(interface {
		IssueApplicationStandardSnapshotCapture(context.Context, string, state.ApplicationStandardSnapshotCaptureRequest) (runtimeadmission.SnapshotGrant, error)
	}); ok {
		return o.IssueApplicationStandardSnapshotCapture(p0, p1, p2)
	}
	return s.MemStore.IssueApplicationStandardSnapshotCapture(p0, p1, p2)
}

func (s *Mem) JobClaimLegacyArtifactVerification(p0 context.Context, p1 int, p2 string, p3 time.Duration) ([]state.Job, error) {
	if o, ok := s.over.(interface {
		JobClaimLegacyArtifactVerification(context.Context, int, string, time.Duration) ([]state.Job, error)
	}); ok {
		return o.JobClaimLegacyArtifactVerification(p0, p1, p2, p3)
	}
	return s.MemStore.JobClaimLegacyArtifactVerification(p0, p1, p2, p3)
}

func (s *Mem) JobClaimPendingImageMaterialization(p0 context.Context, p1 int, p2 string, p3 time.Duration) ([]state.Job, error) {
	if o, ok := s.over.(interface {
		JobClaimPendingImageMaterialization(context.Context, int, string, time.Duration) ([]state.Job, error)
	}); ok {
		return o.JobClaimPendingImageMaterialization(p0, p1, p2, p3)
	}
	return s.MemStore.JobClaimPendingImageMaterialization(p0, p1, p2, p3)
}

func (s *Mem) JobFinishLegacyArtifactVerification(p0 context.Context, p1 string, p2 string, p3 string, p4 string, p5 bool, p6 string) (state.Job, error) {
	if o, ok := s.over.(interface {
		JobFinishLegacyArtifactVerification(context.Context, string, string, string, string, bool, string) (state.Job, error)
	}); ok {
		return o.JobFinishLegacyArtifactVerification(p0, p1, p2, p3, p4, p5, p6)
	}
	return s.MemStore.JobFinishLegacyArtifactVerification(p0, p1, p2, p3, p4, p5, p6)
}

func (s *Mem) JobPublishImageMaterialization(p0 context.Context, p1 string, p2 string, p3 string, p4 int, p5 string, p6 string) (state.Job, error) {
	if o, ok := s.over.(interface {
		JobPublishImageMaterialization(context.Context, string, string, string, int, string, string) (state.Job, error)
	}); ok {
		return o.JobPublishImageMaterialization(p0, p1, p2, p3, p4, p5, p6)
	}
	return s.MemStore.JobPublishImageMaterialization(p0, p1, p2, p3, p4, p5, p6)
}

func (s *Mem) JobRenewImageMaterializationLease(p0 context.Context, p1 string, p2 string, p3 string, p4 int, p5 time.Duration) error {
	if o, ok := s.over.(interface {
		JobRenewImageMaterializationLease(context.Context, string, string, string, int, time.Duration) error
	}); ok {
		return o.JobRenewImageMaterializationLease(p0, p1, p2, p3, p4, p5)
	}
	return s.MemStore.JobRenewImageMaterializationLease(p0, p1, p2, p3, p4, p5)
}

func (s *Mem) JobRetryLegacyArtifactVerification(p0 context.Context, p1 string, p2 string, p3 string, p4 string, p5 time.Time) (state.Job, error) {
	if o, ok := s.over.(interface {
		JobRetryLegacyArtifactVerification(context.Context, string, string, string, string, time.Time) (state.Job, error)
	}); ok {
		return o.JobRetryLegacyArtifactVerification(p0, p1, p2, p3, p4, p5)
	}
	return s.MemStore.JobRetryLegacyArtifactVerification(p0, p1, p2, p3, p4, p5)
}

func (s *Mem) LatestAppEgressPolicyRevision(p0 context.Context, p1 string) (int64, error) {
	if o, ok := s.over.(interface {
		LatestAppEgressPolicyRevision(context.Context, string) (int64, error)
	}); ok {
		return o.LatestAppEgressPolicyRevision(p0, p1)
	}
	return s.MemStore.LatestAppEgressPolicyRevision(p0, p1)
}

func (s *Mem) LatestSnapshotForTier(p0 context.Context, p1 string, p2 string) (state.Snapshot, error) {
	if o, ok := s.over.(interface {
		LatestSnapshotForTier(context.Context, string, string) (state.Snapshot, error)
	}); ok {
		return o.LatestSnapshotForTier(p0, p1, p2)
	}
	return s.MemStore.LatestSnapshotForTier(p0, p1, p2)
}

func (s *Mem) ListAppDeploymentAuditBetween(p0 context.Context, p1 string, p2 string, p3 time.Time, p4 time.Time, p5 int) ([]state.AppChangeRow, error) {
	if o, ok := s.over.(interface {
		ListAppDeploymentAuditBetween(context.Context, string, string, time.Time, time.Time, int) ([]state.AppChangeRow, error)
	}); ok {
		return o.ListAppDeploymentAuditBetween(p0, p1, p2, p3, p4, p5)
	}
	return s.MemStore.ListAppDeploymentAuditBetween(p0, p1, p2, p3, p4, p5)
}

func (s *Mem) ListMCPReadinessMonitoring(p0 context.Context, p1 string, p2 string, p3 string, p4 ...string) ([]api.MCPReadinessMonitorStatus, error) {
	if o, ok := s.over.(interface {
		ListMCPReadinessMonitoring(context.Context, string, string, string, ...string) ([]api.MCPReadinessMonitorStatus, error)
	}); ok {
		return o.ListMCPReadinessMonitoring(p0, p1, p2, p3, p4...)
	}
	return s.MemStore.ListMCPReadinessMonitoring(p0, p1, p2, p3, p4...)
}

func (s *Mem) ListPendingApplicationStandardEgress(p0 context.Context, p1 string) ([]state.ApplicationStandardEgressTarget, error) {
	if o, ok := s.over.(interface {
		ListPendingApplicationStandardEgress(context.Context, string) ([]state.ApplicationStandardEgressTarget, error)
	}); ok {
		return o.ListPendingApplicationStandardEgress(p0, p1)
	}
	return s.MemStore.ListPendingApplicationStandardEgress(p0, p1)
}

func (s *Mem) ListSnapshotsForGC(p0 context.Context) ([]state.SnapshotForGC, error) {
	if o, ok := s.over.(interface {
		ListSnapshotsForGC(context.Context) ([]state.SnapshotForGC, error)
	}); ok {
		return o.ListSnapshotsForGC(p0)
	}
	return s.MemStore.ListSnapshotsForGC(p0)
}

func (s *Mem) MCPConnectionHealth(p0 context.Context, p1 string, p2 string, p3 int, p4 time.Time) (api.MCPConnectionHealthSnapshot, error) {
	if o, ok := s.over.(interface {
		MCPConnectionHealth(context.Context, string, string, int, time.Time) (api.MCPConnectionHealthSnapshot, error)
	}); ok {
		return o.MCPConnectionHealth(p0, p1, p2, p3, p4)
	}
	return s.MemStore.MCPConnectionHealth(p0, p1, p2, p3, p4)
}

func (s *Mem) MarkOldSnapshotsStale(p0 context.Context, p1 []string) (int64, error) {
	if o, ok := s.over.(interface {
		MarkOldSnapshotsStale(context.Context, []string) (int64, error)
	}); ok {
		return o.MarkOldSnapshotsStale(p0, p1)
	}
	return s.MemStore.MarkOldSnapshotsStale(p0, p1)
}

func (s *Mem) PublishApplicationStandardSnapshotCapture(p0 context.Context, p1 runtimeadmission.SnapshotAcknowledgment) error {
	if o, ok := s.over.(interface {
		PublishApplicationStandardSnapshotCapture(context.Context, runtimeadmission.SnapshotAcknowledgment) error
	}); ok {
		return o.PublishApplicationStandardSnapshotCapture(p0, p1)
	}
	return s.MemStore.PublishApplicationStandardSnapshotCapture(p0, p1)
}

func (s *Mem) PublishBaseImageProducer(p0 context.Context, p1 state.BaseImageProducerInput) (state.BaseImageProducer, error) {
	if o, ok := s.over.(interface {
		PublishBaseImageProducer(context.Context, state.BaseImageProducerInput) (state.BaseImageProducer, error)
	}); ok {
		return o.PublishBaseImageProducer(p0, p1)
	}
	return s.MemStore.PublishBaseImageProducer(p0, p1)
}

func (s *Mem) PublishBaseImageScan(p0 context.Context, p1 state.BaseImageScanInput) (state.BaseImageScan, error) {
	if o, ok := s.over.(interface {
		PublishBaseImageScan(context.Context, state.BaseImageScanInput) (state.BaseImageScan, error)
	}); ok {
		return o.PublishBaseImageScan(p0, p1)
	}
	return s.MemStore.PublishBaseImageScan(p0, p1)
}

func (s *Mem) PublishDeploymentRuntimeScan(p0 context.Context, p1 state.DeploymentRuntimeScanInput) (state.DeploymentRuntimeScan, error) {
	if o, ok := s.over.(interface {
		PublishDeploymentRuntimeScan(context.Context, state.DeploymentRuntimeScanInput) (state.DeploymentRuntimeScan, error)
	}); ok {
		return o.PublishDeploymentRuntimeScan(p0, p1)
	}
	return s.MemStore.PublishDeploymentRuntimeScan(p0, p1)
}

func (s *Mem) PublishSnapshotIfRuntimeFresh(p0 context.Context, p1 state.Snapshot, p2 string, p3 time.Time) (state.Snapshot, error) {
	if o, ok := s.over.(interface {
		PublishSnapshotIfRuntimeFresh(context.Context, state.Snapshot, string, time.Time) (state.Snapshot, error)
	}); ok {
		return o.PublishSnapshotIfRuntimeFresh(p0, p1, p2, p3)
	}
	return s.MemStore.PublishSnapshotIfRuntimeFresh(p0, p1, p2, p3)
}

func (s *Mem) RecordApplicationStandardEgress(p0 context.Context, p1 state.ApplicationStandardEgressTarget, p2 runtimeadmission.EgressReceipt) (state.ApplicationStandardEgressObservation, error) {
	if o, ok := s.over.(interface {
		RecordApplicationStandardEgress(context.Context, state.ApplicationStandardEgressTarget, runtimeadmission.EgressReceipt) (state.ApplicationStandardEgressObservation, error)
	}); ok {
		return o.RecordApplicationStandardEgress(p0, p1, p2)
	}
	return s.MemStore.RecordApplicationStandardEgress(p0, p1, p2)
}

func (s *Mem) RecordDeploymentRegistryVerification(p0 context.Context, p1 state.DeploymentRegistryVerificationInput) (state.DeploymentRegistryVerification, error) {
	if o, ok := s.over.(interface {
		RecordDeploymentRegistryVerification(context.Context, state.DeploymentRegistryVerificationInput) (state.DeploymentRegistryVerification, error)
	}); ok {
		return o.RecordDeploymentRegistryVerification(p0, p1)
	}
	return s.MemStore.RecordDeploymentRegistryVerification(p0, p1)
}

func (s *Mem) RuntimeReleaseQualification(p0 context.Context, p1 string) (state.RuntimeReleaseQualification, error) {
	if o, ok := s.over.(interface {
		RuntimeReleaseQualification(context.Context, string) (state.RuntimeReleaseQualification, error)
	}); ok {
		return o.RuntimeReleaseQualification(p0, p1)
	}
	return s.MemStore.RuntimeReleaseQualification(p0, p1)
}

func (s *Mem) SetDeploymentFailed(p0 context.Context, p1 string, p2 string, p3 string) (state.Deployment, error) {
	if o, ok := s.over.(interface {
		SetDeploymentFailed(context.Context, string, string, string) (state.Deployment, error)
	}); ok {
		return o.SetDeploymentFailed(p0, p1, p2, p3)
	}
	return s.MemStore.SetDeploymentFailed(p0, p1, p2, p3)
}

func (s *Mem) UpdateDeploymentHostingVerification(p0 context.Context, p1 string, p2 state.HostingVerificationUpdate) (state.HostingVerificationProgress, error) {
	if o, ok := s.over.(interface {
		UpdateDeploymentHostingVerification(context.Context, string, state.HostingVerificationUpdate) (state.HostingVerificationProgress, error)
	}); ok {
		return o.UpdateDeploymentHostingVerification(p0, p1, p2)
	}
	return s.MemStore.UpdateDeploymentHostingVerification(p0, p1, p2)
}

func (s *Mem) UpsertDeploymentHostingReceipt(p0 context.Context, p1 string, p2 []byte) (state.Deployment, error) {
	if o, ok := s.over.(interface {
		UpsertDeploymentHostingReceipt(context.Context, string, []byte) (state.Deployment, error)
	}); ok {
		return o.UpsertDeploymentHostingReceipt(p0, p1, p2)
	}
	return s.MemStore.UpsertDeploymentHostingReceipt(p0, p1, p2)
}

func (s *Mem) ValidateDeploymentRuntimeUpgradeBaseline(p0 context.Context, p1 string) error {
	if o, ok := s.over.(interface {
		ValidateDeploymentRuntimeUpgradeBaseline(context.Context, string) error
	}); ok {
		return o.ValidateDeploymentRuntimeUpgradeBaseline(p0, p1)
	}
	return s.MemStore.ValidateDeploymentRuntimeUpgradeBaseline(p0, p1)
}

func (s *Mem) WorkflowAlertSnapshot(p0 context.Context, p1 string, p2 string, p3 time.Time, p4 time.Time) (state.WorkflowAlertSnapshot, error) {
	if o, ok := s.over.(interface {
		WorkflowAlertSnapshot(context.Context, string, string, time.Time, time.Time) (state.WorkflowAlertSnapshot, error)
	}); ok {
		return o.WorkflowAlertSnapshot(p0, p1, p2, p3, p4)
	}
	return s.MemStore.WorkflowAlertSnapshot(p0, p1, p2, p3, p4)
}

func (s *Mem) WorkflowRecoveryReviewCompletionNotificationAllowed(p0 context.Context, p1 state.AppWebhookDelivery) (bool, string, error) {
	if o, ok := s.over.(interface {
		WorkflowRecoveryReviewCompletionNotificationAllowed(context.Context, state.AppWebhookDelivery) (bool, string, error)
	}); ok {
		return o.WorkflowRecoveryReviewCompletionNotificationAllowed(p0, p1)
	}
	return s.MemStore.WorkflowRecoveryReviewCompletionNotificationAllowed(p0, p1)
}

func (s *Mem) WorkflowRecoveryReviewNotificationAllowed(p0 context.Context, p1 state.AppWebhookDelivery, p2 time.Time) (bool, string, error) {
	if o, ok := s.over.(interface {
		WorkflowRecoveryReviewNotificationAllowed(context.Context, state.AppWebhookDelivery, time.Time) (bool, string, error)
	}); ok {
		return o.WorkflowRecoveryReviewNotificationAllowed(p0, p1, p2)
	}
	return s.MemStore.WorkflowRecoveryReviewNotificationAllowed(p0, p1, p2)
}

func (s *Store) ClaimDueAppWebhookDeliveries(p0 context.Context, p1 int, p2 time.Time) ([]state.AppWebhookDelivery, error) {
	if o, ok := s.over.(interface {
		ClaimDueAppWebhookDeliveries(context.Context, int, time.Time) ([]state.AppWebhookDelivery, error)
	}); ok {
		return o.ClaimDueAppWebhookDeliveries(p0, p1, p2)
	}
	return s.Store.ClaimDueAppWebhookDeliveries(p0, p1, p2)
}

func (s *Store) CreateInstanceWithMode(p0 context.Context, p1 string, p2 string, p3 string, p4 int, p5 string, p6 string, p7 string) (state.Instance, error) {
	if o, ok := s.over.(interface {
		CreateInstanceWithMode(context.Context, string, string, string, int, string, string, string) (state.Instance, error)
	}); ok {
		return o.CreateInstanceWithMode(p0, p1, p2, p3, p4, p5, p6, p7)
	}
	return s.Store.CreateInstanceWithMode(p0, p1, p2, p3, p4, p5, p6, p7)
}

func (s *Store) ListAppEnvInScope(p0 context.Context, p1 string, p2 string, p3 string) ([]state.AppEnv, error) {
	if o, ok := s.over.(interface {
		ListAppEnvInScope(context.Context, string, string, string) ([]state.AppEnv, error)
	}); ok {
		return o.ListAppEnvInScope(p0, p1, p2, p3)
	}
	return s.Store.ListAppEnvInScope(p0, p1, p2, p3)
}

func (s *Store) ListAppTrustedSignersForApp(p0 context.Context, p1 string) ([]state.AppTrustedSigner, error) {
	if o, ok := s.over.(interface {
		ListAppTrustedSignersForApp(context.Context, string) ([]state.AppTrustedSigner, error)
	}); ok {
		return o.ListAppTrustedSignersForApp(p0, p1)
	}
	return s.Store.ListAppTrustedSignersForApp(p0, p1)
}

func (s *Store) ListDeploymentsForOperator(p0 context.Context, p1 state.OperatorDeploymentFilter) ([]state.Deployment, error) {
	if o, ok := s.over.(interface {
		ListDeploymentsForOperator(context.Context, state.OperatorDeploymentFilter) ([]state.Deployment, error)
	}); ok {
		return o.ListDeploymentsForOperator(p0, p1)
	}
	return s.Store.ListDeploymentsForOperator(p0, p1)
}

func (s *Store) ProjectByOrgSlug(ctx context.Context, orgID, slug string) (state.Project, error) {
	if override, ok := s.over.(interface {
		ProjectByOrgSlug(context.Context, string, string) (state.Project, error)
	}); ok {
		return override.ProjectByOrgSlug(ctx, orgID, slug)
	}
	return s.Store.ProjectByOrgSlug(ctx, orgID, slug)
}

func (s *Store) ListProjectsForOrg(ctx context.Context, orgID string) ([]state.Project, error) {
	if override, ok := s.over.(interface {
		ListProjectsForOrg(context.Context, string) ([]state.Project, error)
	}); ok {
		return override.ListProjectsForOrg(ctx, orgID)
	}
	return s.Store.ListProjectsForOrg(ctx, orgID)
}

func (s *Mem) GetProductBillingDocument(p0 context.Context, p1 state.ProductBillingScope, p2 string) (api.ProductBillingDocument, error) {
	if o, ok := s.over.(interface {
		GetProductBillingDocument(context.Context, state.ProductBillingScope, string) (api.ProductBillingDocument, error)
	}); ok {
		return o.GetProductBillingDocument(p0, p1, p2)
	}
	return s.MemStore.GetProductBillingDocument(p0, p1, p2)
}

func (s *Mem) ListProductBillingDocuments(p0 context.Context, p1 state.ProductBillingScope, p2 string) (api.ProductBillingDocuments, error) {
	if o, ok := s.over.(interface {
		ListProductBillingDocuments(context.Context, state.ProductBillingScope, string) (api.ProductBillingDocuments, error)
	}); ok {
		return o.ListProductBillingDocuments(p0, p1, p2)
	}
	return s.MemStore.ListProductBillingDocuments(p0, p1, p2)
}

func (s *Mem) GetProductBillingHandoff(p0 context.Context, p1 state.ProductBillingScope, p2 string) (api.ProductInvoiceHandoff, error) {
	if o, ok := s.over.(interface {
		GetProductBillingHandoff(context.Context, state.ProductBillingScope, string) (api.ProductInvoiceHandoff, error)
	}); ok {
		return o.GetProductBillingHandoff(p0, p1, p2)
	}
	return s.MemStore.GetProductBillingHandoff(p0, p1, p2)
}

func (s *Mem) HandoffProductBilling(p0 context.Context, p1 state.ProductBillingScope, p2, p3 string) (api.ProductInvoiceHandoff, error) {
	if o, ok := s.over.(interface {
		HandoffProductBilling(context.Context, state.ProductBillingScope, string, string) (api.ProductInvoiceHandoff, error)
	}); ok {
		return o.HandoffProductBilling(p0, p1, p2, p3)
	}
	return s.MemStore.HandoffProductBilling(p0, p1, p2, p3)
}

func (s *Mem) StatementHandoffBlocked(p0 context.Context, p1, p2, p3 string) (bool, error) {
	if o, ok := s.over.(interface {
		StatementHandoffBlocked(context.Context, string, string, string) (bool, error)
	}); ok {
		return o.StatementHandoffBlocked(p0, p1, p2, p3)
	}
	return s.MemStore.StatementHandoffBlocked(p0, p1, p2, p3)
}

func (s *Mem) PutIdempotent(p0 context.Context, p1 string, p2 string, p3 int, p4 []byte) error {
	if o, ok := s.over.(interface {
		PutIdempotent(context.Context, string, string, int, []byte) error
	}); ok {
		return o.PutIdempotent(p0, p1, p2, p3, p4)
	}
	return s.MemStore.PutIdempotent(p0, p1, p2, p3, p4)
}

func (s *Mem) AttachDeploymentSubmission(p0 context.Context, p1 string, p2 string, p3 string) (api.DeploymentSubmissionReceipt, error) {
	if o, ok := s.over.(interface {
		AttachDeploymentSubmission(context.Context, string, string, string) (api.DeploymentSubmissionReceipt, error)
	}); ok {
		return o.AttachDeploymentSubmission(p0, p1, p2, p3)
	}
	return s.MemStore.AttachDeploymentSubmission(p0, p1, p2, p3)
}

func (s *Mem) CreateBuildWithID(p0 context.Context, p1 string, p2 string, p3 state.DeploymentKind, p4 int64, p5 string) (state.Build, error) {
	if o, ok := s.over.(interface {
		CreateBuildWithID(context.Context, string, string, state.DeploymentKind, int64, string) (state.Build, error)
	}); ok {
		return o.CreateBuildWithID(p0, p1, p2, p3, p4, p5)
	}
	return s.MemStore.CreateBuildWithID(p0, p1, p2, p3, p4, p5)
}

func (s *Mem) UpdateProjectEnvironmentPromotionWorkload(p0 context.Context, p1 string, p2 string, p3 string, p4 string, p5 string, p6 string) (state.ProjectEnvironmentPromotionWorkload, error) {
	if o, ok := s.over.(interface {
		UpdateProjectEnvironmentPromotionWorkload(context.Context, string, string, string, string, string, string) (state.ProjectEnvironmentPromotionWorkload, error)
	}); ok {
		return o.UpdateProjectEnvironmentPromotionWorkload(p0, p1, p2, p3, p4, p5, p6)
	}
	return s.MemStore.UpdateProjectEnvironmentPromotionWorkload(p0, p1, p2, p3, p4, p5, p6)
}

func (s *Mem) UpdateProjectEnvironmentPromotionVerification(p0 context.Context, p1 string, p2 string, p3 string, p4 string, p5 *time.Time, p6 *time.Time) (state.ProjectEnvironmentPromotion, error) {
	if o, ok := s.over.(interface {
		UpdateProjectEnvironmentPromotionVerification(context.Context, string, string, string, string, *time.Time, *time.Time) (state.ProjectEnvironmentPromotion, error)
	}); ok {
		return o.UpdateProjectEnvironmentPromotionVerification(p0, p1, p2, p3, p4, p5, p6)
	}
	return s.MemStore.UpdateProjectEnvironmentPromotionVerification(p0, p1, p2, p3, p4, p5, p6)
}

func (s *Mem) ListPendingSafeProjectSubmissions(p0 context.Context) ([]state.SafeProjectSubmission, error) {
	if o, ok := s.over.(interface {
		ListPendingSafeProjectSubmissions(context.Context) ([]state.SafeProjectSubmission, error)
	}); ok {
		return o.ListPendingSafeProjectSubmissions(p0)
	}
	return s.MemStore.ListPendingSafeProjectSubmissions(p0)
}

func (s *Mem) ProjectSubmission(p0 context.Context, p1 string, p2 string) (api.ProjectSubmissionReceipt, error) {
	if o, ok := s.over.(interface {
		ProjectSubmission(context.Context, string, string) (api.ProjectSubmissionReceipt, error)
	}); ok {
		return o.ProjectSubmission(p0, p1, p2)
	}
	return s.MemStore.ProjectSubmission(p0, p1, p2)
}

func (s *Mem) ProjectReleaseSetByID(p0 context.Context, p1 string, p2 string, p3 string, p4 string) (state.ProjectReleaseSet, error) {
	if o, ok := s.over.(interface {
		ProjectReleaseSetByID(context.Context, string, string, string, string) (state.ProjectReleaseSet, error)
	}); ok {
		return o.ProjectReleaseSetByID(p0, p1, p2, p3, p4)
	}
	return s.MemStore.ProjectReleaseSetByID(p0, p1, p2, p3, p4)
}

func (s *Mem) AdvanceProjectReleaseVerification(p0 context.Context, p1 string, p2 string, p3 *state.ProjectReleaseVerificationRound) (*api.ProjectSafeRelease, error) {
	if o, ok := s.over.(interface {
		AdvanceProjectReleaseVerification(context.Context, string, string, *state.ProjectReleaseVerificationRound) (*api.ProjectSafeRelease, error)
	}); ok {
		return o.AdvanceProjectReleaseVerification(p0, p1, p2, p3)
	}
	return s.MemStore.AdvanceProjectReleaseVerification(p0, p1, p2, p3)
}

func (s *Mem) ReconcileSafeProjectSubmission(p0 context.Context, p1 string, p2 string) (*api.ProjectSafeRelease, error) {
	if o, ok := s.over.(interface {
		ReconcileSafeProjectSubmission(context.Context, string, string) (*api.ProjectSafeRelease, error)
	}); ok {
		return o.ReconcileSafeProjectSubmission(p0, p1, p2)
	}
	return s.MemStore.ReconcileSafeProjectSubmission(p0, p1, p2)
}

func (s *Mem) ReleaseAppTaskByDeployment(p0 context.Context, p1 string) (state.AppTask, error) {
	if o, ok := s.over.(interface {
		ReleaseAppTaskByDeployment(context.Context, string) (state.AppTask, error)
	}); ok {
		return o.ReleaseAppTaskByDeployment(p0, p1)
	}
	return s.MemStore.ReleaseAppTaskByDeployment(p0, p1)
}

func (s *Mem) RecordProjectMigrationBaseline(p0 context.Context, p1 string, p2 string, p3 state.ProjectMigrationBaselineRound) (*api.ProjectSafeRelease, error) {
	if o, ok := s.over.(interface {
		RecordProjectMigrationBaseline(context.Context, string, string, state.ProjectMigrationBaselineRound) (*api.ProjectSafeRelease, error)
	}); ok {
		return o.RecordProjectMigrationBaseline(p0, p1, p2, p3)
	}
	return s.MemStore.RecordProjectMigrationBaseline(p0, p1, p2, p3)
}

func (s *Mem) PinnedProjectReleaseWorkers(p0 context.Context, p1 string) ([]string, error) {
	if o, ok := s.over.(interface {
		PinnedProjectReleaseWorkers(context.Context, string) ([]string, error)
	}); ok {
		return o.PinnedProjectReleaseWorkers(p0, p1)
	}
	return s.MemStore.PinnedProjectReleaseWorkers(p0, p1)
}

func (s *Mem) HeldProjectReleaseWorkers(p0 context.Context, p1 string) ([]string, error) {
	if o, ok := s.over.(interface {
		HeldProjectReleaseWorkers(context.Context, string) ([]string, error)
	}); ok {
		return o.HeldProjectReleaseWorkers(p0, p1)
	}
	return s.MemStore.HeldProjectReleaseWorkers(p0, p1)
}
