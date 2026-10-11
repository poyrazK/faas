from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.app_health_history_page import AppHealthHistoryPage
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    limit: int | Unset = 20,
    before: UUID | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    json_before: str | Unset = UNSET
    if not isinstance(before, Unset):
        json_before = str(before)
    params["before"] = json_before

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/health/history".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppHealthHistoryPage | Problem:
    if response.status_code == 200:
        response_200 = AppHealthHistoryPage.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppHealthHistoryPage | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    before: UUID | Unset = UNSET,
) -> Response[AppHealthHistoryPage | Problem]:
    """Read recorded app health changes.

     Requires apps:read or admin; no MFA required. Background collection
    records default HTTP serving assessments independently of dashboard
    reads, without waking or probing workloads. Baseline, meaningful
    changes and expired-evidence gaps are newest first. Times describe
    observations or evidence expiry, not exact incident start/end times.
    Retains up to 100 entries within 4 MiB and 30 days per app; each entry
    is bounded to 64 KiB. Missing, foreign, aged or pruned cursors return
    404. Latest retains its original time; collector_fresh is false when
    unavailable or expired. Reads never create or refresh stored evidence.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppHealthHistoryPage | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        limit=limit,
        before=before,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    before: UUID | Unset = UNSET,
) -> AppHealthHistoryPage | Problem | None:
    """Read recorded app health changes.

     Requires apps:read or admin; no MFA required. Background collection
    records default HTTP serving assessments independently of dashboard
    reads, without waking or probing workloads. Baseline, meaningful
    changes and expired-evidence gaps are newest first. Times describe
    observations or evidence expiry, not exact incident start/end times.
    Retains up to 100 entries within 4 MiB and 30 days per app; each entry
    is bounded to 64 KiB. Missing, foreign, aged or pruned cursors return
    404. Latest retains its original time; collector_fresh is false when
    unavailable or expired. Reads never create or refresh stored evidence.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppHealthHistoryPage | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        limit=limit,
        before=before,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    before: UUID | Unset = UNSET,
) -> Response[AppHealthHistoryPage | Problem]:
    """Read recorded app health changes.

     Requires apps:read or admin; no MFA required. Background collection
    records default HTTP serving assessments independently of dashboard
    reads, without waking or probing workloads. Baseline, meaningful
    changes and expired-evidence gaps are newest first. Times describe
    observations or evidence expiry, not exact incident start/end times.
    Retains up to 100 entries within 4 MiB and 30 days per app; each entry
    is bounded to 64 KiB. Missing, foreign, aged or pruned cursors return
    404. Latest retains its original time; collector_fresh is false when
    unavailable or expired. Reads never create or refresh stored evidence.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppHealthHistoryPage | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        limit=limit,
        before=before,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    before: UUID | Unset = UNSET,
) -> AppHealthHistoryPage | Problem | None:
    """Read recorded app health changes.

     Requires apps:read or admin; no MFA required. Background collection
    records default HTTP serving assessments independently of dashboard
    reads, without waking or probing workloads. Baseline, meaningful
    changes and expired-evidence gaps are newest first. Times describe
    observations or evidence expiry, not exact incident start/end times.
    Retains up to 100 entries within 4 MiB and 30 days per app; each entry
    is bounded to 64 KiB. Missing, foreign, aged or pruned cursors return
    404. Latest retains its original time; collector_fresh is false when
    unavailable or expired. Reads never create or refresh stored evidence.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppHealthHistoryPage | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            limit=limit,
            before=before,
        )
    ).parsed
