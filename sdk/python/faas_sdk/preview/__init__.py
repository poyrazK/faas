"""faas_sdk.preview - the complete Python SDK (ADR-20261023115741).

`faas_sdk` covers generally available and beta capabilities. This package
adds every preview and internal capability: the complete generated client
from `api/openapi.yaml` under `faas_sdk.preview.api` and
`faas_sdk.preview.models`, and the helpers built on it. It re-exports
`faas_sdk` too; its `FaaSClient` adds the disposable Runs helpers. apid
serves a preview capability only to the accounts it is released to;
elsewhere its calls answer 404 no such API route.
"""

# ruff: noqa: F401
from .. import (
    DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE,
    GREGALE_RELEASE_HEADER,
    GREGALE_REQUEST_DEADLINE_HEADER,
    GREGALE_REVISION_HEADER,
    PRE_AUTH_TARGET_HEADER,
    WEBHOOK_DELIVERY_ID_HEADER,
    WEBHOOK_SIGNATURE_HEADER,
    WEBHOOK_TIMESTAMP_HEADER,
    AsyncGregaleReleaseTransport,
    AuthenticatedClient,
    Client,
    ErrCapacity,
    ErrNotFound,
    ErrRateLimited,
    ErrUnauthorized,
    FaaSClient,
    FaaSClientOptions,
    FaasError,
    FaasProblemError,
    GregaleReleaseMiddleware,
    GregaleReleaseTransport,
    IdempotencyKey,
    Problem,
    RetryOptions,
    SseEvent,
    VerifiedWebhook,
    WebhookVerificationError,
    WrapperOptions,
    __version__,
    aiter_sse,
    as_faas_error,
    current_gregale_release,
    current_gregale_request_deadline,
    current_idempotency_key,
    install_chain,
    is_faas_error,
    iter_sse,
    mint_idempotency_key,
    parse_problem,
    pre_auth_target_digest,
    raise_for_problem,
    verify_webhook,
    with_gregale_release,
    with_gregale_request_context,
    with_idempotency_key,
)
from .. import __all__ as _PACKAGE_ALL
from ._binding_creation import (
    BindingVerificationCreationError,
    acreate_binding_verification_from_review,
    acreate_project_binding_verification_from_review,
    areconcile_binding_verification_creation,
    areconcile_project_binding_verification_creation,
    create_binding_verification_from_review,
    create_project_binding_verification_from_review,
    reconcile_binding_verification_creation,
    reconcile_project_binding_verification_creation,
)
from ._binding_runner import (
    BindingVerificationProgress,
    BindingVerificationRecoveryError,
    arun_binding_verification,
    arun_project_binding_verification,
    run_binding_verification,
    run_project_binding_verification,
)
from ._event_acknowledgment_protocol import (
    EventAcknowledgmentWorkerCancellation,
    EventAcknowledgmentWorkerCancelled,
    EventAcknowledgmentWorkerError,
    EventAcknowledgmentWorkerOptions,
    EventAcknowledgmentWorkerResult,
    EventAcknowledgmentWorkerStatus,
)
from ._event_deadline_policy import (
    EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX,
    EVENT_DEADLINE_REASONS,
    EventDeadlinePolicyOptions,
    EventDeadlineRatioOptions,
    EventDeadlineTarget,
)
from ._wrapper import FaaSClient
from .ai_gateway import (
    AI_SESSION_HEADER,
    AI_USER_HEADER,
    AIGatewayError,
    ai_gateway_headers,
    ai_gateway_url,
    is_valid_ai_user_id,
    opaque_ai_user_id,
)
from .commit import CommitEventRouting, ainsert_commit_event, insert_commit_event
from .commit_delivery_report import CommitDeliveryReportError, aget_commit_delivery_report
from .commit_recovery_apply import CommitRecoveryApplyError, aapply_commit_recovery
from .commit_recovery_plan import CommitRecoveryPlanError, aplan_commit_recovery
from .commit_recovery_status import (
    CommitRecoveryStatusError,
    adiagnose_commit_recovery_status,
    aget_commit_recovery_status,
    awatch_commit_recovery_status,
    commit_recovery_status_settled,
)
from .commit_subscriber_wait import await_commit_subscriber
from .crash_snapshots import (
    CRASH_SNAPSHOT_ENDPOINT,
    CrashSnapshotResult,
    acapture_crash_snapshot,
    capture_crash_snapshot,
)
from .customer_operations import (
    CustomerOperationConflictError,
    CustomerOperationPublicationError,
    CustomerOperationTransaction,
)
from .data_repair import awith_data_repair_capture, with_data_repair_capture
from .data_repair_readiness import acheck_data_repair_capture, check_data_repair_capture
from .data_repair_transactions import (
    DataRepairCaptureError,
    awith_repairable_customer_operation_transaction,
    awith_repairable_operation_transaction,
    with_repairable_customer_operation_transaction,
    with_repairable_operation_transaction,
)
from .dev_bridge import (
    DEV_BRIDGE_CONTEXT_HEADER,
    AsyncDevBridgeTransport,
    DevBridgeMiddleware,
    DevBridgeTransport,
    current_dev_bridge_context,
    with_dev_bridge_context,
)
from .event_acknowledgment_intent_budget_adoption import (
    EventAcknowledgmentIntentAdoptRetryBudgetOptions,
    EventAcknowledgmentIntentBudgetAdoptionError,
    EventAcknowledgmentIntentBudgetAdoptionReceipt,
    aadopt_event_acknowledgment_intent_retry_budget,
    aget_event_acknowledgment_intent_budget_adoption_candidate,
    event_acknowledgment_intent_budget_adoption_schema,
)
from .event_acknowledgment_intent_checkpoint import (
    EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX,
    acknowledge_event_acknowledgment_intent_alerts,
    validate_event_acknowledgment_intent_checkpoint,
)
from .event_acknowledgment_intent_checkpoint_watch import (
    EventAcknowledgmentIntentCheckpointWatchCallbackError,
    EventAcknowledgmentIntentCheckpointWatchCancelled,
    EventAcknowledgmentIntentCheckpointWatchOptions,
    awatch_event_acknowledgment_intent_health_with_checkpoint,
)
from .event_acknowledgment_intent_health import (
    EventAcknowledgmentIntentHealth,
    EventAcknowledgmentIntentHealthOptions,
    aget_event_acknowledgment_intent_health,
)
from .event_acknowledgment_intent_health_metrics import (
    EventAcknowledgmentIntentHealthMetrics,
    EventAcknowledgmentIntentHealthMetricsOptions,
    EventAcknowledgmentIntentHealthMetricsSample,
    EventAcknowledgmentIntentHealthMetricsSnapshot,
)
from .event_acknowledgment_intent_health_metrics_watch import awatch_event_acknowledgment_intent_health_metrics
from .event_acknowledgment_intent_health_prometheus import (
    EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE,
    format_event_acknowledgment_intent_health_metrics,
)
from .event_acknowledgment_intent_policy_adoption import (
    EventAcknowledgmentIntentAdoptRetryPolicyOptions,
    EventAcknowledgmentIntentPolicyAdoptionError,
    EventAcknowledgmentIntentPolicyAdoptionReceipt,
    aadopt_event_acknowledgment_intent_retry_policy,
    event_acknowledgment_intent_policy_adoption_schema,
)
from .event_acknowledgment_intent_recovery import (
    EventAcknowledgmentIntentClaim,
    aclaim_event_acknowledgment_intent,
    arecover_next_event_acknowledgment_intent,
)
from .event_acknowledgment_intent_recovery_pass import (
    EventAcknowledgmentIntentRecoveryPassCancelled,
    EventAcknowledgmentIntentRecoveryPassCommitUnknownError,
    EventAcknowledgmentIntentRecoveryPassError,
    EventAcknowledgmentIntentRecoveryPassOptions,
    EventAcknowledgmentIntentRecoveryPassResult,
    arecover_event_acknowledgment_intents,
)
from .event_acknowledgment_intent_retry_policy import (
    EventAcknowledgmentIntentRetryBudget,
    EventAcknowledgmentIntentRetryPolicy,
    EventAcknowledgmentIntentStoredRetryPolicy,
)
from .event_acknowledgment_intent_retry_resume import (
    EventAcknowledgmentIntentResumeRetryBudgetOptions,
    EventAcknowledgmentIntentRetryResumeError,
    EventAcknowledgmentIntentRetryResumeReceipt,
    aget_event_acknowledgment_intent_retry_resume_candidate,
    aresume_event_acknowledgment_intent_retry_budget,
    event_acknowledgment_intent_retry_budget_schema,
    event_acknowledgment_intent_retry_resume_schema,
)
from .event_acknowledgment_intent_watch import (
    EventAcknowledgmentIntentAlert,
    EventAcknowledgmentIntentWatchCallbackError,
    EventAcknowledgmentIntentWatchOptions,
    awatch_event_acknowledgment_intent_health,
)
from .event_acknowledgment_intents import (
    EventAcknowledgmentIntentError,
    EventAcknowledgmentIntentParkedError,
    EventAcknowledgmentIntentResult,
    EventAcknowledgmentIntentStatus,
    alist_event_acknowledgment_intents,
    event_acknowledgment_intent_schema,
    event_acknowledgment_intent_stored_retry_policy_schema,
)
from .event_acknowledgment_preparation import EventAcknowledgmentPreparedOptions
from .event_acknowledgment_preparation_events import (
    EventAcknowledgmentPreparationEvent,
    EventAcknowledgmentPreparationOutcome,
    EventAcknowledgmentPreparationReason,
    EventAcknowledgmentPreparationStage,
)
from .event_acknowledgment_preparation_metrics import (
    EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS,
    EventAcknowledgmentPreparationHistogram,
    EventAcknowledgmentPreparationMetrics,
    EventAcknowledgmentPreparationMetricsSnapshot,
)
from .event_acknowledgment_preparation_prometheus import (
    EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE,
    format_event_acknowledgment_preparation_metrics,
)
from .event_acknowledgment_worker import AsyncEventAcknowledgmentWorker
from .event_consumer_delivery_context import (
    EventConsumerDeliveryContext,
    EventConsumerDeliveryContextUnavailableError,
    event_consumer_delivery_context_from_headers,
)
from .event_consumer_processing import (
    EventConsumerProcessingError,
    EventConsumerProcessingLease,
    aclaim_event_consumer_processing,
    event_consumer_processing_schema,
)
from .event_consumer_processing_inspection import (
    EventConsumerProcessingInspection,
    ainspect_event_consumer_processing,
)
from .event_consumer_transactions import (
    EventConsumerCommitUnknownError,
    EventConsumerConflictError,
    EventConsumerRequest,
    EventConsumerTransactionResult,
    awith_event_consumer_transaction,
    event_consumer_receipt_schema,
    event_consumer_request_from_headers,
    new_event_consumer_request,
    with_event_consumer_transaction,
)
from .event_deadline_checkpoint import acknowledge_event_deadline_alerts, validate_event_deadline_checkpoint
from .event_deadline_investigation import (
    EventDeadlineInvestigationOptions,
    EventDeadlineInvestigationReadError,
    ainvestigate_event_deadline_alert,
)
from .event_deadline_watch import (
    EventDeadlineWatchCallbackError,
    EventDeadlineWatchCancelled,
    EventDeadlineWatchOptions,
    awatch_event_subscription_deadlines,
)
from .executions import ExecutionEvent, ExecutionID, awatch_execution, decode_execution_artifact, watch_execution
from .flags import (
    GREGALE_FLAG_CONTEXT_HEADER,
    GREGALE_FLAG_EVIDENCE_HEADER,
    GREGALE_FLAG_PROPAGATION_HEADER,
    AsyncGregaleFlagsTransport,
    FlagDecision,
    GregaleFlags,
    GregaleFlagsMiddleware,
    evaluate_flag,
    evaluate_variant,
    flag_bucket,
    flag_subject_bucket,
    flag_subject_variant_bucket,
    flag_variant_bucket,
    validate_bundle,
)
from .issues import IssueReporter
from .operations import (
    CustomerOperationRequest,
    OperationCommitUnknownError,
    OperationConflictError,
    OperationEffect,
    OperationOutcome,
    OperationRequest,
    OperationTransactionResult,
    awith_customer_operation_transaction,
    awith_operation_transaction,
    customer_operation_receipt_schema,
    customer_operation_request_digest,
    customer_operation_request_from_headers,
    operation_receipt_schema,
    operation_request_digest,
    operation_request_from_headers,
    with_customer_operation_transaction,
    with_operation_transaction,
)
from .operations_runtime import GregaleOperations, OperationExecutionContext, OperationHTTPError
from .realtime_activity import RealtimeActivity, RealtimeActivityTracker
from .realtime_backpressure import RealtimeBackpressureMonitor, RealtimeBackpressureStatus, RealtimeProcessingStatus
from .realtime_catch_up import RealtimeCatchUpStatus, RealtimeCatchUpTracker, RealtimeCatchUpWaitError
from .realtime_channels import RealtimeChannelSubscription, RealtimeMultiChannelClient
from .realtime_connection_status import (
    RealtimeConnectionQuality,
    RealtimeConnectionQualityTracker,
    RealtimeConnectionStatus,
    RealtimeConnectionStatusTracker,
)
from .realtime_diagnostics import export_realtime_diagnostics
from .realtime_direct_messages import RealtimeDirectMessage, RealtimeWebSocketDirectMessages
from .realtime_drain import RealtimeDrainResult
from .realtime_expiry import RealtimeExpiryDiagnostics, RealtimeExpiryStatus, RealtimeMessageExpiry
from .realtime_filters import RealtimeEventFilter
from .realtime_health import RealtimeHealthSummary, summarize_realtime_health
from .realtime_presence import (
    RealtimeActivityParticipant,
    RealtimeActivitySummary,
    RealtimeLabeledActivityParticipant,
    RealtimePresenceDirectory,
    RealtimePresenceEvent,
    RealtimePresenceMember,
    RealtimePresenceSummary,
    format_realtime_typing_summary,
    summarize_realtime_activity,
)
from .realtime_publisher import (
    RealtimeActivityPublisher,
    RealtimeActivityPublishError,
    create_managed_realtime_activity_publisher,
)
from .realtime_read_progress import (
    RealtimeReadProgress,
    RealtimeReadProgressFreshness,
    RealtimeReadProgressTracker,
    RealtimeUnreadBadge,
    RealtimeUnreadSummary,
    format_realtime_unread_badge,
    summarize_realtime_unread,
)
from .realtime_read_publisher import (
    RealtimeReadProgressPublisher,
    RealtimeReadPublishError,
    create_managed_realtime_read_publisher,
)
from .realtime_read_refresh import RealtimeReadRefreshCoordinator, RealtimeReadRefreshError
from .realtime_readiness import (
    RealtimeCatchUpRequirement,
    RealtimeReadinessError,
    RealtimeReadinessResult,
    wait_realtime_ready,
)
from .realtime_recovery import (
    RealtimeRecoveryCoordinator,
    RealtimeRecoveryError,
    RealtimeRecoveryEvent,
    RealtimeRecoveryRetry,
    RealtimeRecoverySession,
    RealtimeRecoveryTarget,
)
from .realtime_scopes import (
    REALTIME_ACTIVITY_SCOPE_PREFIX,
    parse_realtime_activity_scope,
    realtime_activity_scope_channel,
)
from .realtime_sse import RealtimeSSEClient, RealtimeSSEMessage
from .realtime_subscriptions import (
    RealtimeSubscriptionStatus,
    RealtimeSubscriptionSummary,
    RealtimeSubscriptionTracker,
    RealtimeSubscriptionWaitError,
)
from .realtime_timeline import RealtimeIncidentEvent, RealtimeIncidentTimeline
from .realtime_transport import RealtimeFallbackClient, RealtimeTransportStatus
from .realtime_websocket import RealtimeWebSocketClient, RealtimeWebSocketMessage, RealtimeWebSocketSession
from .realtime_websocket_presence import RealtimeWebSocketPresence, RealtimeWebSocketSignal
from .realtime_websocket_reads import RealtimeWebSocketReadProgress
from .realtime_workers import (
    RealtimeLeaseLostError,
    RealtimeWorkerContext,
    RealtimeWorkerError,
    RealtimeWorkerEvent,
    RealtimeWorkerOptions,
    consume_realtime_consumer_group,
    consume_realtime_consumer_group_with_credential,
)

__all__ = (
    *_PACKAGE_ALL,
    "AI_SESSION_HEADER",
    "AI_USER_HEADER",
    "AIGatewayError",
    "ai_gateway_headers",
    "ai_gateway_url",
    "is_valid_ai_user_id",
    "opaque_ai_user_id",
    "IssueReporter",
    "GREGALE_FLAG_CONTEXT_HEADER",
    "GREGALE_FLAG_EVIDENCE_HEADER",
    "GREGALE_FLAG_PROPAGATION_HEADER",
    "GregaleFlags",
    "GregaleFlagsMiddleware",
    "AsyncGregaleFlagsTransport",
    "FlagDecision",
    "evaluate_flag",
    "evaluate_variant",
    "flag_bucket",
    "flag_subject_bucket",
    "flag_subject_variant_bucket",
    "flag_variant_bucket",
    "validate_bundle",
    "CRASH_SNAPSHOT_ENDPOINT",
    "CrashSnapshotResult",
    "capture_crash_snapshot",
    "acapture_crash_snapshot",
    "ExecutionEvent",
    "ExecutionID",
    "watch_execution",
    "awatch_execution",
    "decode_execution_artifact",
    "DEV_BRIDGE_CONTEXT_HEADER",
    "AsyncDevBridgeTransport",
    "DevBridgeMiddleware",
    "DevBridgeTransport",
    "current_dev_bridge_context",
    "with_dev_bridge_context",
    "insert_commit_event",
    "ainsert_commit_event",
    "CommitEventRouting",
    "CustomerOperationRequest",
    "customer_operation_request_from_headers",
    "customer_operation_request_digest",
    "customer_operation_receipt_schema",
    "with_customer_operation_transaction",
    "awith_customer_operation_transaction",
    "OperationCommitUnknownError",
    "OperationConflictError",
    "OperationEffect",
    "OperationOutcome",
    "OperationRequest",
    "OperationTransactionResult",
    "awith_operation_transaction",
    "operation_receipt_schema",
    "operation_request_digest",
    "operation_request_from_headers",
    "with_operation_transaction",
    "with_data_repair_capture",
    "awith_data_repair_capture",
    "check_data_repair_capture",
    "acheck_data_repair_capture",
    "DataRepairCaptureError",
    "with_repairable_operation_transaction",
    "awith_repairable_operation_transaction",
    "with_repairable_customer_operation_transaction",
    "awith_repairable_customer_operation_transaction",
    "CustomerOperationConflictError",
    "CustomerOperationPublicationError",
    "CustomerOperationRequest",
    "CustomerOperationTransaction",
    "awith_customer_operation_transaction",
    "customer_operation_receipt_schema",
    "customer_operation_request_digest",
    "customer_operation_request_from_headers",
    "GregaleOperations",
    "OperationExecutionContext",
    "OperationHTTPError",
    "RealtimeDrainResult",
    "RealtimeBackpressureMonitor",
    "RealtimeBackpressureStatus",
    "RealtimeProcessingStatus",
    "RealtimeExpiryDiagnostics",
    "RealtimeExpiryStatus",
    "RealtimeMessageExpiry",
    "RealtimeSSEClient",
    "RealtimeSSEMessage",
    "RealtimeFallbackClient",
    "RealtimeTransportStatus",
    "RealtimeDirectMessage",
    "RealtimeWebSocketDirectMessages",
    "RealtimeWebSocketReadProgress",
    "RealtimeEventFilter",
    "RealtimeWebSocketPresence",
    "RealtimeWebSocketSignal",
    "RealtimeChannelSubscription",
    "RealtimeMultiChannelClient",
    "RealtimeWebSocketClient",
    "RealtimeWebSocketMessage",
    "RealtimeWebSocketSession",
    "RealtimeRecoveryCoordinator",
    "RealtimeRecoveryError",
    "RealtimeRecoveryEvent",
    "RealtimeRecoveryRetry",
    "RealtimeRecoverySession",
    "RealtimeRecoveryTarget",
    "RealtimeIncidentEvent",
    "RealtimeIncidentTimeline",
    "export_realtime_diagnostics",
    "RealtimeCatchUpRequirement",
    "RealtimeReadinessError",
    "RealtimeReadinessResult",
    "wait_realtime_ready",
    "RealtimeSubscriptionStatus",
    "RealtimeSubscriptionSummary",
    "RealtimeSubscriptionTracker",
    "RealtimeSubscriptionWaitError",
    "RealtimeCatchUpStatus",
    "RealtimeCatchUpTracker",
    "RealtimeCatchUpWaitError",
    "RealtimeHealthSummary",
    "summarize_realtime_health",
    "RealtimeConnectionStatus",
    "RealtimeConnectionStatusTracker",
    "RealtimeConnectionQuality",
    "RealtimeConnectionQualityTracker",
    "RealtimeReadRefreshCoordinator",
    "RealtimeReadRefreshError",
    "RealtimeReadProgressPublisher",
    "RealtimeReadPublishError",
    "create_managed_realtime_read_publisher",
    "RealtimeReadProgress",
    "RealtimeReadProgressTracker",
    "RealtimeReadProgressFreshness",
    "RealtimeUnreadSummary",
    "RealtimeUnreadBadge",
    "format_realtime_unread_badge",
    "summarize_realtime_unread",
    "REALTIME_ACTIVITY_SCOPE_PREFIX",
    "parse_realtime_activity_scope",
    "realtime_activity_scope_channel",
    "RealtimeActivityPublishError",
    "RealtimeActivityPublisher",
    "create_managed_realtime_activity_publisher",
    "RealtimeActivitySummary",
    "RealtimeLabeledActivityParticipant",
    "summarize_realtime_activity",
    "format_realtime_typing_summary",
    "RealtimeActivityParticipant",
    "RealtimePresenceDirectory",
    "RealtimePresenceEvent",
    "RealtimePresenceMember",
    "RealtimePresenceSummary",
    "RealtimeActivity",
    "RealtimeActivityTracker",
    "RealtimeLeaseLostError",
    "RealtimeWorkerContext",
    "RealtimeWorkerError",
    "RealtimeWorkerEvent",
    "RealtimeWorkerOptions",
    "consume_realtime_consumer_group",
    "consume_realtime_consumer_group_with_credential",
    "EventConsumerCommitUnknownError",
    "EventConsumerConflictError",
    "EventConsumerRequest",
    "EventConsumerTransactionResult",
    "awith_event_consumer_transaction",
    "event_consumer_receipt_schema",
    "event_consumer_request_from_headers",
    "new_event_consumer_request",
    "with_event_consumer_transaction",
    "await_commit_subscriber",
    "CommitDeliveryReportError",
    "aget_commit_delivery_report",
    "CommitRecoveryPlanError",
    "aplan_commit_recovery",
    "CommitRecoveryApplyError",
    "aapply_commit_recovery",
    "adiagnose_commit_recovery_status",
    "CommitRecoveryStatusError",
    "aget_commit_recovery_status",
    "awatch_commit_recovery_status",
    "commit_recovery_status_settled",
    "BindingVerificationRecoveryError",
    "BindingVerificationProgress",
    "run_binding_verification",
    "run_project_binding_verification",
    "arun_binding_verification",
    "arun_project_binding_verification",
    "BindingVerificationCreationError",
    "create_binding_verification_from_review",
    "reconcile_binding_verification_creation",
    "create_project_binding_verification_from_review",
    "reconcile_project_binding_verification_creation",
    "acreate_binding_verification_from_review",
    "areconcile_binding_verification_creation",
    "acreate_project_binding_verification_from_review",
    "areconcile_project_binding_verification_creation",
    "EventConsumerDeliveryContext",
    "EventConsumerDeliveryContextUnavailableError",
    "event_consumer_delivery_context_from_headers",
    "EVENT_DEADLINE_REASONS",
    "EventDeadlinePolicyOptions",
    "EventDeadlineTarget",
    "EventDeadlineWatchOptions",
    "EventDeadlineWatchCallbackError",
    "EventDeadlineWatchCancelled",
    "awatch_event_subscription_deadlines",
    "validate_event_deadline_checkpoint",
    "acknowledge_event_deadline_alerts",
    "EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX",
    "EventDeadlineRatioOptions",
    "EventDeadlineInvestigationOptions",
    "EventDeadlineInvestigationReadError",
    "ainvestigate_event_deadline_alert",
    "AsyncEventAcknowledgmentWorker",
    "EventAcknowledgmentWorkerOptions",
    "EventAcknowledgmentWorkerResult",
    "EventAcknowledgmentWorkerStatus",
    "EventAcknowledgmentWorkerError",
    "EventAcknowledgmentWorkerCancelled",
    "EventAcknowledgmentWorkerCancellation",
    "EventAcknowledgmentIntentError",
    "EventAcknowledgmentIntentResult",
    "EventAcknowledgmentIntentStatus",
    "EventAcknowledgmentIntentClaim",
    "aclaim_event_acknowledgment_intent",
    "arecover_next_event_acknowledgment_intent",
    "alist_event_acknowledgment_intents",
    "event_acknowledgment_intent_schema",
    "EventAcknowledgmentIntentRetryPolicy",
    "EventAcknowledgmentIntentRetryBudget",
    "EventAcknowledgmentIntentParkedError",
    "EventAcknowledgmentIntentStoredRetryPolicy",
    "event_acknowledgment_intent_stored_retry_policy_schema",
    "EventAcknowledgmentIntentAdoptRetryPolicyOptions",
    "EventAcknowledgmentIntentPolicyAdoptionError",
    "EventAcknowledgmentIntentPolicyAdoptionReceipt",
    "aadopt_event_acknowledgment_intent_retry_policy",
    "event_acknowledgment_intent_policy_adoption_schema",
    "EventAcknowledgmentIntentResumeRetryBudgetOptions",
    "EventAcknowledgmentIntentRetryResumeError",
    "EventAcknowledgmentIntentRetryResumeReceipt",
    "aget_event_acknowledgment_intent_retry_resume_candidate",
    "aresume_event_acknowledgment_intent_retry_budget",
    "event_acknowledgment_intent_retry_budget_schema",
    "event_acknowledgment_intent_retry_resume_schema",
    "EventAcknowledgmentIntentAdoptRetryBudgetOptions",
    "EventAcknowledgmentIntentBudgetAdoptionError",
    "EventAcknowledgmentIntentBudgetAdoptionReceipt",
    "aadopt_event_acknowledgment_intent_retry_budget",
    "aget_event_acknowledgment_intent_budget_adoption_candidate",
    "event_acknowledgment_intent_budget_adoption_schema",
    "EventAcknowledgmentIntentHealth",
    "EventAcknowledgmentIntentHealthOptions",
    "aget_event_acknowledgment_intent_health",
    "EventAcknowledgmentIntentHealthMetrics",
    "EventAcknowledgmentIntentHealthMetricsOptions",
    "EventAcknowledgmentIntentHealthMetricsSample",
    "EventAcknowledgmentIntentHealthMetricsSnapshot",
    "awatch_event_acknowledgment_intent_health_metrics",
    "EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE",
    "format_event_acknowledgment_intent_health_metrics",
    "EventAcknowledgmentIntentAlert",
    "EventAcknowledgmentIntentWatchCallbackError",
    "EventAcknowledgmentIntentWatchOptions",
    "awatch_event_acknowledgment_intent_health",
    "EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX",
    "acknowledge_event_acknowledgment_intent_alerts",
    "validate_event_acknowledgment_intent_checkpoint",
    "EventAcknowledgmentIntentCheckpointWatchCallbackError",
    "EventAcknowledgmentIntentCheckpointWatchCancelled",
    "EventAcknowledgmentIntentCheckpointWatchOptions",
    "awatch_event_acknowledgment_intent_health_with_checkpoint",
    "EventConsumerProcessingError",
    "EventConsumerProcessingLease",
    "aclaim_event_consumer_processing",
    "event_consumer_processing_schema",
    "EventAcknowledgmentPreparedOptions",
    "EventConsumerProcessingInspection",
    "ainspect_event_consumer_processing",
    "EventAcknowledgmentPreparationEvent",
    "EventAcknowledgmentPreparationOutcome",
    "EventAcknowledgmentPreparationReason",
    "EventAcknowledgmentPreparationStage",
    "EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS",
    "EventAcknowledgmentPreparationHistogram",
    "EventAcknowledgmentPreparationMetrics",
    "EventAcknowledgmentPreparationMetricsSnapshot",
    "EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE",
    "format_event_acknowledgment_preparation_metrics",
    "EventAcknowledgmentIntentRecoveryPassCancelled",
    "EventAcknowledgmentIntentRecoveryPassCommitUnknownError",
    "EventAcknowledgmentIntentRecoveryPassError",
    "EventAcknowledgmentIntentRecoveryPassOptions",
    "EventAcknowledgmentIntentRecoveryPassResult",
    "arecover_event_acknowledgment_intents",
)


def __getattr__(name: str):
    # The package's lazily loaded model classes (faas_sdk._LAZY_MODELS).
    from .. import _LAZY_MODELS

    if name not in _LAZY_MODELS:
        raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
    from importlib import import_module

    return getattr(import_module("faas_sdk"), name)

