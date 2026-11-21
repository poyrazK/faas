"""faas_sdk - Python client for the Gregale API.

This package covers generally available and beta capabilities
(ADR-20261023115741): the client generated from `sdk/openapi.yaml` under
`faas_sdk.api` and `faas_sdk.models`, and the helpers below. Preview and
internal capabilities (realtime, operations, events, durable entities,
queues, object storage, managed Postgres, disposable runs, issues, commit,
data repair, AI gateway, flags, and more) are in `faas_sdk.preview`, which
re-exports this package too.

Public surface:

* `FaaSClient` - the recommended entry point. Constructs the
  generator's `Client` and installs the wrapper BaseTransport
  chain (retry -> logging -> rfc7807 -> idempotency) on top of
  its inner httpx client.
* `FaaSClientOptions` - knobs for retry / logger.
* `IdempotencyKey`, `with_idempotency_key` - opt-in idempotency
  scoping (mirrors Go's `faas.WithIdempotencyKey` and the Node
  SDK's `withIdempotencyKey`).
* `Problem`, `FaasError`, `ErrNotFound`, `ErrUnauthorized`,
  `ErrRateLimited`, `ErrCapacity`, `as_faas_error`,
  `is_faas_error` - RFC 7807 problem-decoding + four canonical
  sentinels.
* `SseEvent`, `iter_sse`, `aiter_sse` - Server-Sent Events
  parser for the long-lived `/v1/apps/{slug}/logs` endpoint.
* `GregaleReleaseMiddleware` and HTTPX transports - capture and forward the
  request's project release to managed service calls.
* `verify_webhook` - verify signed outbound deliveries against their raw body
  and return the stable delivery ID for receiver-side deduplication.
* `pre_auth_target_digest` - opaque login-target signal for selected failed
  responses on opt-in pre-auth routes.
* `function` - decorator declaring a function's deploy settings next to
  its handler (cron, queue, RAM, ...); `gregale deploy` reads it statically.
"""

from ._async_request import RequestOptions, RequestTimeoutError, ResponseInfo, ResponseObserver, RetryPolicy
from ._pagination import AsyncPagination, PaginationError, PaginationOptions, PaginationPage
from ._project_inspection import (
    ProjectBuild,
    ProjectCandidate,
    ProjectFramework,
    ProjectGeneratedFile,
    ProjectInput,
    ProjectInspectionError,
    ProjectInspectionOptions,
    ProjectIssue,
    ProjectIssueCode,
    ProjectPlan,
    ProjectSetting,
    ProjectShape,
    inspect_project,
    validate_project_plan,
)
from ._project_verification import (
    ProjectVerificationOptions as ProjectVerificationOptions,
)
from ._project_verification import (
    ProjectVerificationReport as ProjectVerificationReport,
)
from ._project_verification import (
    ProjectVerificationTarget as ProjectVerificationTarget,
)
from ._project_verification import (
    VerificationCheck as VerificationCheck,
)
from ._project_verification import (
    VerificationCheckContext as VerificationCheckContext,
)
from ._project_verification import (
    VerificationCheckEvidence as VerificationCheckEvidence,
)
from ._project_verification import (
    VerificationCheckResult as VerificationCheckResult,
)
from ._project_verification import (
    VerificationCode as VerificationCode,
)
from ._project_verification import (
    VerificationHealth as VerificationHealth,
)
from ._project_verification import (
    VerificationOutcome as VerificationOutcome,
)
from ._project_verification import (
    VerificationTimeline as VerificationTimeline,
)
from ._project_verification import (
    VerificationTimelineEvent as VerificationTimelineEvent,
)
from ._project_verification import (
    VerificationWorkload as VerificationWorkload,
)
from ._project_verification import (
    VerificationWorkloadReport as VerificationWorkloadReport,
)
from ._project_verification import (
    project_verification_target as project_verification_target,
)
from ._project_workflows import (
    ProjectApplicationResult as ProjectApplicationResult,
)
from ._project_workflows import (
    ProjectApplicationState as ProjectApplicationState,
)
from ._project_workflows import (
    ProjectApplyOptions as ProjectApplyOptions,
)
from ._project_workflows import (
    ProjectArchive as ProjectArchive,
)
from ._project_workflows import (
    ProjectPreviewOptions as ProjectPreviewOptions,
)
from ._project_workflows import (
    ProjectWorkflowCode as ProjectWorkflowCode,
)
from ._project_workflows import (
    ProjectWorkflowError as ProjectWorkflowError,
)
from ._project_workflows import (
    ProjectWorkflowOptions as ProjectWorkflowOptions,
)
from ._project_workflows import (
    ProjectWorkflowPhase as ProjectWorkflowPhase,
)
from ._project_workflows import (
    ProjectWorkflowProgress as ProjectWorkflowProgress,
)
from ._project_workflows import (
    ProjectWorkloadOutcome as ProjectWorkloadOutcome,
)
from ._project_workflows import (
    ProjectWorkloadStatus as ProjectWorkloadStatus,
)
from ._project_workflows import (
    ReviewedProjectPlan as ReviewedProjectPlan,
)
from ._project_workflows import (
    load_project_application_state as load_project_application_state,
)
from ._rfc7807 import (
    ErrCapacity,
    ErrNotFound,
    ErrRateLimited,
    ErrUnauthorized,
    FaasError,
    FaasProblemError,
    Problem,
    as_faas_error,
    is_faas_error,
    parse_problem,
    raise_for_problem,
)
from ._source_deployment import (
    SourceDeploymentArchive,
    SourceDeploymentCancelledError,
    SourceDeploymentCode,
    SourceDeploymentError,
    SourceDeploymentOptions,
    SourceDeploymentPhase,
    SourceDeploymentPreparationOptions,
    SourceDeploymentProgress,
    SourceDeploymentReleaseProgress,
    SourceDeploymentResult,
    SourceDeploymentState,
    SourceDeploymentWaitOptions,
    load_source_deployment_state,
)
from ._source_preparation import (
    PreparedSource,
    SourceEntry,
    SourceIssue,
    SourcePlan,
    SourcePreparationCode,
    SourcePreparationError,
    SourcePreparationOptions,
    SourcePreparationProgress,
    inspect_source,
    prepare_source,
)
from ._source_upload import (
    SourceUploadCheckpoint,
    SourceUploadCode,
    SourceUploadError,
    SourceUploadOptions,
    SourceUploadProgress,
    SourceUploadStage,
    SourceUploadTimeoutError,
    load_source_upload_checkpoint,
    save_source_upload_checkpoint,
)
from ._sse import SseEvent, aiter_sse, iter_sse
from ._transport import RetryOptions, WrapperOptions, install_chain
from ._typed_jobs import (
    JobCheckpoint as JobCheckpoint,
)
from ._typed_jobs import (
    JobCheckpointInput as JobCheckpointInput,
)
from ._typed_jobs import (
    JobItemOutcome as JobItemOutcome,
)
from ._typed_jobs import (
    JobProgress as JobProgress,
)
from ._typed_jobs import (
    JobRunOptions as JobRunOptions,
)
from ._typed_jobs import (
    JobSubmitOptions as JobSubmitOptions,
)
from ._typed_jobs import (
    TypedJob as TypedJob,
)
from ._typed_jobs import (
    TypedJobRun as TypedJobRun,
)
from ._waiters import (
    BuildWaitOptions,
    DeploymentWaitOptions,
    WaiterError,
    WaiterFailureCode,
    WaiterOptions,
    WaiterProgress,
    WaiterTimeoutError,
)
from ._wrapper import FaaSClient, FaaSClientOptions
from .client import AuthenticatedClient, Client
from .functions import FunctionHookContext, function
from .idempotency import (
    IdempotencyKey,
    current_idempotency_key,
    mint_idempotency_key,
    with_idempotency_key,
)
from .job_runtime import (
    JobCode as JobCode,
)
from .job_runtime import (
    JobContext as JobContext,
)
from .job_runtime import (
    JobContract as JobContract,
)
from .job_runtime import (
    JobError as JobError,
)
from .job_runtime import (
    JobInput as JobInput,
)
from .job_runtime import (
    JobInputBinding as JobInputBinding,
)
from .job_runtime import (
    JobStore as JobStore,
)
from .job_runtime import (
    LocalJobOutcome as LocalJobOutcome,
)
from .job_runtime import (
    LocalJobStore as LocalJobStore,
)
from .job_runtime import (
    decode_job_output as decode_job_output,
)
from .job_runtime import (
    prepare_job_input as prepare_job_input,
)
from .job_runtime import (
    read_job_input as read_job_input,
)
from .job_runtime import (
    run_job_task as run_job_task,
)
from .job_runtime import (
    run_local_job_batch as run_local_job_batch,
)
from .pre_auth_target import PRE_AUTH_TARGET_HEADER, pre_auth_target_digest
from .release_context import (
    GREGALE_DEADLINE_MS_HEADER,
    GREGALE_RELEASE_HEADER,
    GREGALE_REQUEST_DEADLINE_HEADER,
    GREGALE_REVISION_HEADER,
    AsyncGregaleReleaseTransport,
    GregaleReleaseMiddleware,
    GregaleReleaseTransport,
    current_gregale_release,
    current_gregale_request_deadline,
    gregale_request_time_remaining_ms,
    with_gregale_release,
    with_gregale_request_context,
)
from .webhook import (
    DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE,
    WEBHOOK_DELIVERY_ID_HEADER,
    WEBHOOK_PREVIOUS_SIGNATURE_HEADER,
    WEBHOOK_SIGNATURE_HEADER,
    WEBHOOK_TIMESTAMP_HEADER,
    VerifiedWebhook,
    WebhookVerificationError,
    verify_webhook,
)

__version__ = "0.1.0"

# Generated model classes load on first use (PEP 562), so `import faas_sdk`
# does not import every generated model, and `faas_sdk.preview` does not
# load this package's models beside its own.
_LAZY_MODELS = {
    "OperationWorkflowBlocker": ".models.operation_workflow_blocker",
    "OperationWorkflowBlockerResolution": ".models.operation_workflow_blocker_resolution",
    "OperationWorkflowAttentionStats": ".models.operation_workflow_attention_stats",
    "OperationWorkflowAttentionGroup": ".models.operation_workflow_attention_group",
    "OperationWorkflowAttentionSummary": ".models.operation_workflow_attention_summary",
    "OperationWorkflowOutcomeEntry": ".models.operation_workflow_outcome_entry",
    "OperationWorkflowOutcomesResponse": ".models.operation_workflow_outcomes_response",
    "OperationWorkflowOutcomeGroup": ".models.operation_workflow_outcome_group",
    "OperationWorkflowOutcomeSummary": ".models.operation_workflow_outcome_summary",
    "OperationWorkflowDependency": ".models.operation_workflow_dependency",
    "OperationWorkflowRelatedInstance": ".models.operation_workflow_related_instance",
}


def __getattr__(name: str):
    module = _LAZY_MODELS.get(name)
    if module is None:
        raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
    from importlib import import_module

    value = getattr(import_module(module, __name__), name)
    globals()[name] = value
    return value


def __dir__() -> list[str]:
    return sorted({*globals(), *_LAZY_MODELS})


__all__ = (
    "ProjectArchive",
    "ReviewedProjectPlan",
    "ProjectWorkflowOptions",
    "ProjectPreviewOptions",
    "ProjectApplyOptions",
    "ProjectApplicationState",
    "ProjectWorkflowCode",
    "ProjectWorkflowPhase",
    "ProjectWorkflowProgress",
    "ProjectWorkflowError",
    "ProjectApplicationResult",
    "ProjectWorkloadOutcome",
    "ProjectWorkloadStatus",
    "load_project_application_state",
    "function",
    "FunctionHookContext",
    "FaaSClient",
    "FaaSClientOptions",
    "Client",
    "AuthenticatedClient",
    "AsyncPagination",
    "PaginationError",
    "PaginationOptions",
    "PaginationPage",
    "inspect_source",
    "prepare_source",
    "SourcePreparationError",
    "SourcePreparationCode",
    "SourcePreparationOptions",
    "SourcePreparationProgress",
    "SourceIssue",
    "SourceEntry",
    "SourcePlan",
    "PreparedSource",
    "inspect_project",
    "validate_project_plan",
    "ProjectPlan",
    "ProjectInspectionOptions",
    "ProjectInspectionError",
    "ProjectShape",
    "ProjectFramework",
    "ProjectIssue",
    "ProjectIssueCode",
    "ProjectInput",
    "ProjectSetting",
    "ProjectCandidate",
    "ProjectGeneratedFile",
    "ProjectBuild",
    "SourceDeploymentArchive",
    "SourceDeploymentCode",
    "SourceDeploymentPhase",
    "SourceDeploymentState",
    "SourceDeploymentPreparationOptions",
    "SourceDeploymentWaitOptions",
    "SourceDeploymentOptions",
    "SourceDeploymentProgress",
    "SourceDeploymentReleaseProgress",
    "SourceDeploymentResult",
    "SourceDeploymentError",
    "SourceDeploymentCancelledError",
    "load_source_deployment_state",

    "SourceUploadCheckpoint",
    "SourceUploadCode",
    "SourceUploadError",
    "SourceUploadOptions",
    "SourceUploadProgress",
    "SourceUploadStage",
    "SourceUploadTimeoutError",
    "load_source_upload_checkpoint",
    "save_source_upload_checkpoint",

    "BuildWaitOptions",
    "DeploymentWaitOptions",
    "WaiterError",
    "WaiterFailureCode",
    "WaiterOptions",
    "WaiterProgress",
    "WaiterTimeoutError",

    "RequestOptions",
    "RequestTimeoutError",
    "ResponseInfo",
    "ResponseObserver",
    "RetryPolicy",
    "RetryOptions",
    "WrapperOptions",
    "install_chain",
    "IdempotencyKey",
    "with_idempotency_key",
    "mint_idempotency_key",
    "current_idempotency_key",
    "GREGALE_DEADLINE_MS_HEADER",
    "GREGALE_RELEASE_HEADER",
    "GREGALE_REQUEST_DEADLINE_HEADER",
    "current_gregale_request_deadline",
    "gregale_request_time_remaining_ms",
    "with_gregale_request_context",
    "GREGALE_REVISION_HEADER",
    "GregaleReleaseMiddleware",
    "GregaleReleaseTransport",
    "AsyncGregaleReleaseTransport",
    "current_gregale_release",
    "with_gregale_release",
    "verify_webhook",
    "VerifiedWebhook",
    "WebhookVerificationError",
    "WEBHOOK_PREVIOUS_SIGNATURE_HEADER",
    "WEBHOOK_SIGNATURE_HEADER",
    "WEBHOOK_TIMESTAMP_HEADER",
    "WEBHOOK_DELIVERY_ID_HEADER",
    "DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE",
    "PRE_AUTH_TARGET_HEADER",
    "pre_auth_target_digest",
    "Problem",
    "FaasError",
    "FaasProblemError",
    "ErrNotFound",
    "ErrUnauthorized",
    "ErrRateLimited",
    "ErrCapacity",
    "as_faas_error",
    "is_faas_error",
    "parse_problem",
    "raise_for_problem",
    "SseEvent",
    "iter_sse",
    "aiter_sse",
    "__version__",
    "OperationWorkflowBlockerResolution",
    "OperationWorkflowBlocker",
    "OperationWorkflowAttentionStats",
    "OperationWorkflowAttentionGroup",
    "OperationWorkflowAttentionSummary",
    "OperationWorkflowOutcomeEntry",
    "OperationWorkflowOutcomesResponse",
    "OperationWorkflowOutcomeGroup",
    "OperationWorkflowOutcomeSummary",
    "OperationWorkflowDependency",
    "OperationWorkflowRelatedInstance",
)
