"""faas_sdk._rfc7807 - RFC 7807 problem-decoding and sentinels.

Lives outside `faas_sdk.errors` (which is owned by the generator and
ships a thin `UnexpectedStatus` only). This module defines the public
`FaasError` surface and the four canonical sentinels.

Wire shape: every error response the daemon emits is a
`application/problem+json` body shaped after RFC 7807 with platform
extensions:

* `code` - stable enum (`not_found`, `unauthorized`, `rate_limited`,
  `capacity_unavailable`, etc.). Used by the four sentinels below.
* `status` - HTTP status code (matches the response status).
* `title`, `detail` - human-readable.
* `instance` - request URI (optional).
* `tx_id` - server-side trace id (optional).
* `limit`, `observed`, `docs_url` - capacity errors carry these.
* `checkout_url`, `billing_portal_url`, `paddle_checkout_url` -
  payment-required errors. `paddle_checkout_url` is retained for
  backwards compatibility; new clients should use `checkout_url`.

The four sentinels mirror `pkg/api/errors.go` +
`internal/api/apierror.go::Unwrap` and the Node SDK's
`src/errors.ts`. Use `as_faas_error(err)` to type-narrow an
exception; use `is_faas_error(err, ErrNotFound)` to check membership
(mirrors Go's `errors.Is(err, faas.ErrNotFound)`).
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Protocol, runtime_checkable


@dataclass(frozen=True)
class Problem:
    """RFC 7807 + platform-extensions Problem body."""

    type: str
    title: str
    status: int
    detail: str | None = None
    instance: str | None = None
    code: str | None = None
    tx_id: str | None = None
    limit: int | None = None
    observed: int | None = None
    docs_url: str | None = None
    checkout_url: str | None = None
    billing_portal_url: str | None = None
    paddle_checkout_url: str | None = None


@runtime_checkable
class FaasError(Protocol):
    """Public error surface. `as_faas_error(err)` returns a
    `FaasError` when the underlying exception has Problem shape."""

    problem: Problem
    status: int
    tx_id: str | None


class FaasProblemError(Exception):
    """Concrete `FaasError` raised by the wrapper's rfc7807 layer."""

    def __init__(self, problem: Problem) -> None:
        self.problem = problem
        self.status = problem.status
        self.tx_id = problem.tx_id
        super().__init__(
            f"{problem.status} {problem.title}: {problem.detail or ''}".rstrip()
        )


#: `code: "not_found"` -> `ErrNotFound` (resource 404).
class ErrNotFound(FaasProblemError):
    pass


#: `code: "unauthorized"` -> `ErrUnauthorized` (401 + bad token).
class ErrUnauthorized(FaasProblemError):
    pass


#: `code: "rate_limited"` -> `ErrRateLimited` (429).
class ErrRateLimited(FaasProblemError):
    pass


#: `code: "capacity_unavailable"` -> `ErrCapacity` (503 + scale ceiling).
class ErrCapacity(FaasProblemError):
    pass


# Mapping from RFC 7807 `code` to concrete exception class. Mirrors
# `pkg/api/errors.go::Code*` consts and the Node SDK's
# `src/errors.ts`.
_SENTINEL_BY_CODE: dict[str, type[FaasProblemError]] = {
    "not_found": ErrNotFound,
    "unauthorized": ErrUnauthorized,
    "rate_limited": ErrRateLimited,
    "capacity_unavailable": ErrCapacity,
}


def parse_problem(body: bytes, content_type: str, status: int) -> Problem | None:
    """Decode a `application/problem+json` (or `application/json`)
    body. Returns `None` if the body is not a Problem shape - the
    caller decides whether to raise a generic HTTP error or pass
    through.
    """
    if not body:
        return None
    ct = content_type.split(";", 1)[0].strip().lower()
    if ct not in ("application/problem+json", "application/json"):
        return None
    import json

    try:
        data = json.loads(body)
    except (ValueError, UnicodeDecodeError):
        return None
    if not isinstance(data, dict):
        return None
    ptype = data.get("type") or "about:blank"
    pstatus = int(data.get("status") or status)
    return Problem(
        type=ptype,
        title=data.get("title", ""),
        status=pstatus,
        detail=data.get("detail"),
        instance=data.get("instance"),
        code=data.get("code"),
        tx_id=data.get("tx_id"),
        limit=data.get("limit"),
        observed=data.get("observed"),
        docs_url=data.get("docs_url"),
        checkout_url=data.get("checkout_url"),
        billing_portal_url=data.get("billing_portal_url"),
        paddle_checkout_url=data.get("paddle_checkout_url"),
    )


def raise_for_problem(problem: Problem) -> None:
    """Raise the canonical sentinel for a Problem, or
    `FaasProblemError`.
    """
    if problem.code is not None:
        cls = _SENTINEL_BY_CODE.get(problem.code)
        if cls is not None:
            raise cls(problem)
    raise FaasProblemError(problem)


def as_faas_error(err: BaseException) -> FaasError | None:
    """Type-narrow an exception to `FaasError` if it carries a
    Problem."""
    if isinstance(err, FaasProblemError):
        return err
    return None


def is_faas_error(err: BaseException, *sentinels: type[FaasProblemError]) -> bool:
    """`is_faas_error(err, ErrNotFound)` mirrors Go's
    `errors.Is(err, faas.ErrNotFound)`. With no sentinels, returns
    True for any `FaasProblemError`. With one or more sentinels,
    returns True only if the exception is an instance of one of
    them.
    """
    if not isinstance(err, FaasProblemError):
        return False
    if not sentinels:
        return True
    return isinstance(err, sentinels)


__all__ = [
    "Problem",
    "FaasError",
    "FaasProblemError",
    "ErrNotFound",
    "ErrUnauthorized",
    "ErrRateLimited",
    "ErrCapacity",
    "parse_problem",
    "raise_for_problem",
    "as_faas_error",
    "is_faas_error",
]
