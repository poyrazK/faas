from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_retention_health import EventRetentionHealth
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    source: str | Unset = UNSET,
    app: str | Unset = UNSET,
    window: str | Unset = "24h",
    limit: int | Unset = 100,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["source"] = source

    params["app"] = app

    params["window"] = window

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/events/retention",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventRetentionHealth | Problem | None:
    if response.status_code == 200:
        response_200 = EventRetentionHealth.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventRetentionHealth | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    source: str | Unset = UNSET,
    app: str | Unset = UNSET,
    window: str | Unset = "24h",
    limit: int | Unset = 100,
) -> Response[EventRetentionHealth | Problem]:
    """Inspect receipt pruning eligibility, retention holds and account storage.

     Read-only account snapshot under apps:read/admin scopes and MFA.
    Receipt retention is 30 days after routing settles; unsettled receipts
    have no pruning deadline. Reports current eligible, upcoming expiring
    and held receipts using the pruning worker's shared hold predicate.
    Running backfills pin their acceptance ranges; retained completed
    backfills pin retryable failed items. Opted-in active recovery jobs hold
    pending items until admission or job expiry. Backfill reasons take
    precedence over recovery_pending in primary hold counts. Eligible
    means the nominal deadline has passed without a current hold, not
    that pruning will occur immediately.
    Source/app filters affect receipt counts and samples only. Storage
    usage and utilization always cover the entire account; utilization
    is the maximum of count and byte percentages and can exceed 100 after
    a plan downgrade. Samples are bounded and ordered by nominal deadline,
    source and id. No payloads, work keys, mutations or reservations.

    Args:
        source (str | Unset):
        app (str | Unset):
        window (str | Unset):  Default: '24h'.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRetentionHealth | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        app=app,
        window=window,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    source: str | Unset = UNSET,
    app: str | Unset = UNSET,
    window: str | Unset = "24h",
    limit: int | Unset = 100,
) -> EventRetentionHealth | Problem | None:
    """Inspect receipt pruning eligibility, retention holds and account storage.

     Read-only account snapshot under apps:read/admin scopes and MFA.
    Receipt retention is 30 days after routing settles; unsettled receipts
    have no pruning deadline. Reports current eligible, upcoming expiring
    and held receipts using the pruning worker's shared hold predicate.
    Running backfills pin their acceptance ranges; retained completed
    backfills pin retryable failed items. Opted-in active recovery jobs hold
    pending items until admission or job expiry. Backfill reasons take
    precedence over recovery_pending in primary hold counts. Eligible
    means the nominal deadline has passed without a current hold, not
    that pruning will occur immediately.
    Source/app filters affect receipt counts and samples only. Storage
    usage and utilization always cover the entire account; utilization
    is the maximum of count and byte percentages and can exceed 100 after
    a plan downgrade. Samples are bounded and ordered by nominal deadline,
    source and id. No payloads, work keys, mutations or reservations.

    Args:
        source (str | Unset):
        app (str | Unset):
        window (str | Unset):  Default: '24h'.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRetentionHealth | Problem
    """

    return sync_detailed(
        client=client,
        source=source,
        app=app,
        window=window,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    source: str | Unset = UNSET,
    app: str | Unset = UNSET,
    window: str | Unset = "24h",
    limit: int | Unset = 100,
) -> Response[EventRetentionHealth | Problem]:
    """Inspect receipt pruning eligibility, retention holds and account storage.

     Read-only account snapshot under apps:read/admin scopes and MFA.
    Receipt retention is 30 days after routing settles; unsettled receipts
    have no pruning deadline. Reports current eligible, upcoming expiring
    and held receipts using the pruning worker's shared hold predicate.
    Running backfills pin their acceptance ranges; retained completed
    backfills pin retryable failed items. Opted-in active recovery jobs hold
    pending items until admission or job expiry. Backfill reasons take
    precedence over recovery_pending in primary hold counts. Eligible
    means the nominal deadline has passed without a current hold, not
    that pruning will occur immediately.
    Source/app filters affect receipt counts and samples only. Storage
    usage and utilization always cover the entire account; utilization
    is the maximum of count and byte percentages and can exceed 100 after
    a plan downgrade. Samples are bounded and ordered by nominal deadline,
    source and id. No payloads, work keys, mutations or reservations.

    Args:
        source (str | Unset):
        app (str | Unset):
        window (str | Unset):  Default: '24h'.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRetentionHealth | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        app=app,
        window=window,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    source: str | Unset = UNSET,
    app: str | Unset = UNSET,
    window: str | Unset = "24h",
    limit: int | Unset = 100,
) -> EventRetentionHealth | Problem | None:
    """Inspect receipt pruning eligibility, retention holds and account storage.

     Read-only account snapshot under apps:read/admin scopes and MFA.
    Receipt retention is 30 days after routing settles; unsettled receipts
    have no pruning deadline. Reports current eligible, upcoming expiring
    and held receipts using the pruning worker's shared hold predicate.
    Running backfills pin their acceptance ranges; retained completed
    backfills pin retryable failed items. Opted-in active recovery jobs hold
    pending items until admission or job expiry. Backfill reasons take
    precedence over recovery_pending in primary hold counts. Eligible
    means the nominal deadline has passed without a current hold, not
    that pruning will occur immediately.
    Source/app filters affect receipt counts and samples only. Storage
    usage and utilization always cover the entire account; utilization
    is the maximum of count and byte percentages and can exceed 100 after
    a plan downgrade. Samples are bounded and ordered by nominal deadline,
    source and id. No payloads, work keys, mutations or reservations.

    Args:
        source (str | Unset):
        app (str | Unset):
        window (str | Unset):  Default: '24h'.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRetentionHealth | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            source=source,
            app=app,
            window=window,
            limit=limit,
        )
    ).parsed
