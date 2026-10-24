from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.build_list_response import BuildListResponse
from ...models.get_builds_status import GetBuildsStatus
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app: str | Unset = UNSET,
    status: GetBuildsStatus | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["app"] = app

    json_status: str | Unset = UNSET
    if not isinstance(status, Unset):
        json_status = status

    params["status"] = json_status

    params["before"] = before

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/builds",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BuildListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = BuildListResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[BuildListResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
    status: GetBuildsStatus | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[BuildListResponse | Problem]:
    """List builds (operator view).

     Returns every build the authenticated account owns, ordered
    started_at DESC (nulls last — queued builds stay at the
    bottom of the first page). Optional ?app=<slug> narrows to
    one app; optional ?status=<s> filters to the 4-value status
    enum (queued|running|succeeded|failed; omit for any status).
    Cursor pagination via ?before=<opaque token>; limit defaults
    to 50, capped at 200.

    The response shape mirrors /v1/deployments: items + a
    next_before cursor (empty when end of list). The cursor is
    the opaque tuple `<rfc3339nano>|<id_hex>` of the LAST row
    on this page — server-emitted, round-tripped verbatim. The
    id tiebreaker makes the keyset deterministic for queued
    tails (started_at IS NULL) and for sub-second collisions
    on started_at. See ADR-091 §3.

    BuildResponse.started_at (the per-row wire field) is
    RFC3339 (whole-second) for backward compatibility with
    `GET /v1/builds/{id}`. The cursor's started_at segment is
    RFC3339Nano (sub-second preserved) so the keyset
    sub-second clause is reachable on rows whose started_at
    falls in the same wall-clock second. The two are
    deliberately different and the cursor's higher precision
    is intentional.

    Args:
        app (str | Unset):
        status (GetBuildsStatus | Unset):
        before (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BuildListResponse | Problem]
    """

    kwargs = _get_kwargs(
        app=app,
        status=status,
        before=before,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
    status: GetBuildsStatus | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> BuildListResponse | Problem | None:
    """List builds (operator view).

     Returns every build the authenticated account owns, ordered
    started_at DESC (nulls last — queued builds stay at the
    bottom of the first page). Optional ?app=<slug> narrows to
    one app; optional ?status=<s> filters to the 4-value status
    enum (queued|running|succeeded|failed; omit for any status).
    Cursor pagination via ?before=<opaque token>; limit defaults
    to 50, capped at 200.

    The response shape mirrors /v1/deployments: items + a
    next_before cursor (empty when end of list). The cursor is
    the opaque tuple `<rfc3339nano>|<id_hex>` of the LAST row
    on this page — server-emitted, round-tripped verbatim. The
    id tiebreaker makes the keyset deterministic for queued
    tails (started_at IS NULL) and for sub-second collisions
    on started_at. See ADR-091 §3.

    BuildResponse.started_at (the per-row wire field) is
    RFC3339 (whole-second) for backward compatibility with
    `GET /v1/builds/{id}`. The cursor's started_at segment is
    RFC3339Nano (sub-second preserved) so the keyset
    sub-second clause is reachable on rows whose started_at
    falls in the same wall-clock second. The two are
    deliberately different and the cursor's higher precision
    is intentional.

    Args:
        app (str | Unset):
        status (GetBuildsStatus | Unset):
        before (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BuildListResponse | Problem
    """

    return sync_detailed(
        client=client,
        app=app,
        status=status,
        before=before,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
    status: GetBuildsStatus | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[BuildListResponse | Problem]:
    """List builds (operator view).

     Returns every build the authenticated account owns, ordered
    started_at DESC (nulls last — queued builds stay at the
    bottom of the first page). Optional ?app=<slug> narrows to
    one app; optional ?status=<s> filters to the 4-value status
    enum (queued|running|succeeded|failed; omit for any status).
    Cursor pagination via ?before=<opaque token>; limit defaults
    to 50, capped at 200.

    The response shape mirrors /v1/deployments: items + a
    next_before cursor (empty when end of list). The cursor is
    the opaque tuple `<rfc3339nano>|<id_hex>` of the LAST row
    on this page — server-emitted, round-tripped verbatim. The
    id tiebreaker makes the keyset deterministic for queued
    tails (started_at IS NULL) and for sub-second collisions
    on started_at. See ADR-091 §3.

    BuildResponse.started_at (the per-row wire field) is
    RFC3339 (whole-second) for backward compatibility with
    `GET /v1/builds/{id}`. The cursor's started_at segment is
    RFC3339Nano (sub-second preserved) so the keyset
    sub-second clause is reachable on rows whose started_at
    falls in the same wall-clock second. The two are
    deliberately different and the cursor's higher precision
    is intentional.

    Args:
        app (str | Unset):
        status (GetBuildsStatus | Unset):
        before (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BuildListResponse | Problem]
    """

    kwargs = _get_kwargs(
        app=app,
        status=status,
        before=before,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
    status: GetBuildsStatus | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> BuildListResponse | Problem | None:
    """List builds (operator view).

     Returns every build the authenticated account owns, ordered
    started_at DESC (nulls last — queued builds stay at the
    bottom of the first page). Optional ?app=<slug> narrows to
    one app; optional ?status=<s> filters to the 4-value status
    enum (queued|running|succeeded|failed; omit for any status).
    Cursor pagination via ?before=<opaque token>; limit defaults
    to 50, capped at 200.

    The response shape mirrors /v1/deployments: items + a
    next_before cursor (empty when end of list). The cursor is
    the opaque tuple `<rfc3339nano>|<id_hex>` of the LAST row
    on this page — server-emitted, round-tripped verbatim. The
    id tiebreaker makes the keyset deterministic for queued
    tails (started_at IS NULL) and for sub-second collisions
    on started_at. See ADR-091 §3.

    BuildResponse.started_at (the per-row wire field) is
    RFC3339 (whole-second) for backward compatibility with
    `GET /v1/builds/{id}`. The cursor's started_at segment is
    RFC3339Nano (sub-second preserved) so the keyset
    sub-second clause is reachable on rows whose started_at
    falls in the same wall-clock second. The two are
    deliberately different and the cursor's higher precision
    is intentional.

    Args:
        app (str | Unset):
        status (GetBuildsStatus | Unset):
        before (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BuildListResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            app=app,
            status=status,
            before=before,
            limit=limit,
        )
    ).parsed
