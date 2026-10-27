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
"""

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
from ._sse import SseEvent, aiter_sse, iter_sse
from ._transport import RetryOptions, WrapperOptions, install_chain
from ._wrapper import FaaSClient, FaaSClientOptions
from .client import AuthenticatedClient, Client
from .idempotency import (
    IdempotencyKey,
    current_idempotency_key,
    mint_idempotency_key,
    with_idempotency_key,
)
from .pre_auth_target import PRE_AUTH_TARGET_HEADER, pre_auth_target_digest
from .release_context import (
    GREGALE_RELEASE_HEADER,
    GREGALE_REQUEST_DEADLINE_HEADER,
    GREGALE_REVISION_HEADER,
    AsyncGregaleReleaseTransport,
    GregaleReleaseMiddleware,
    GregaleReleaseTransport,
    current_gregale_release,
    current_gregale_request_deadline,
    with_gregale_release,
    with_gregale_request_context,
)
from .webhook import (
    DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE,
    WEBHOOK_DELIVERY_ID_HEADER,
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
    "FaaSClient",
    "FaaSClientOptions",
    "Client",
    "AuthenticatedClient",
    "RetryOptions",
    "WrapperOptions",
    "install_chain",
    "IdempotencyKey",
    "with_idempotency_key",
    "mint_idempotency_key",
    "current_idempotency_key",
    "GREGALE_RELEASE_HEADER",
    "GREGALE_REQUEST_DEADLINE_HEADER",
    "current_gregale_request_deadline",
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
