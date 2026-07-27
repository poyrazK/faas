"""faas_sdk - Python client for the one-box FaaS REST API.

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

__version__ = "0.1.0"

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
)
