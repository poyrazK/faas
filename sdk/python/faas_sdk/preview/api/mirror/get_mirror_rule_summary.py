from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_mirror_rule_summary_window import GetMirrorRuleSummaryWindow
from ...models.mirror_summary_response import MirrorSummaryResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: str,
    *,
    window: GetMirrorRuleSummaryWindow | Unset = "1h",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_window: str | Unset = UNSET
    if not isinstance(window, Unset):
        json_window = window

    params["window"] = json_window

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/mirrors/{id}/summary".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MirrorSummaryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = MirrorSummaryResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[MirrorSummaryResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    window: GetMirrorRuleSummaryWindow | Unset = "1h",
) -> Response[MirrorSummaryResponse | Problem]:
    """Aggregate mirror drift counts over a window.

     Read-only aggregate. Source: `mirror_invocation_results` rows
    whose `completed_at >= now - window_seconds`. Returns:
    total invocations, complete changed-response count and percent,
    status/schema/body diff counts, incomplete comparisons, mean/p99
    latency delta, and mirror crash count. Incomplete rows are excluded
    from the changed-response percentage denominator.

    Args:
        slug (str):
        id (str):
        window (GetMirrorRuleSummaryWindow | Unset):  Default: '1h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MirrorSummaryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        window=window,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    window: GetMirrorRuleSummaryWindow | Unset = "1h",
) -> MirrorSummaryResponse | Problem | None:
    """Aggregate mirror drift counts over a window.

     Read-only aggregate. Source: `mirror_invocation_results` rows
    whose `completed_at >= now - window_seconds`. Returns:
    total invocations, complete changed-response count and percent,
    status/schema/body diff counts, incomplete comparisons, mean/p99
    latency delta, and mirror crash count. Incomplete rows are excluded
    from the changed-response percentage denominator.

    Args:
        slug (str):
        id (str):
        window (GetMirrorRuleSummaryWindow | Unset):  Default: '1h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MirrorSummaryResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        window=window,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    window: GetMirrorRuleSummaryWindow | Unset = "1h",
) -> Response[MirrorSummaryResponse | Problem]:
    """Aggregate mirror drift counts over a window.

     Read-only aggregate. Source: `mirror_invocation_results` rows
    whose `completed_at >= now - window_seconds`. Returns:
    total invocations, complete changed-response count and percent,
    status/schema/body diff counts, incomplete comparisons, mean/p99
    latency delta, and mirror crash count. Incomplete rows are excluded
    from the changed-response percentage denominator.

    Args:
        slug (str):
        id (str):
        window (GetMirrorRuleSummaryWindow | Unset):  Default: '1h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MirrorSummaryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        window=window,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    window: GetMirrorRuleSummaryWindow | Unset = "1h",
) -> MirrorSummaryResponse | Problem | None:
    """Aggregate mirror drift counts over a window.

     Read-only aggregate. Source: `mirror_invocation_results` rows
    whose `completed_at >= now - window_seconds`. Returns:
    total invocations, complete changed-response count and percent,
    status/schema/body diff counts, incomplete comparisons, mean/p99
    latency delta, and mirror crash count. Incomplete rows are excluded
    from the changed-response percentage denominator.

    Args:
        slug (str):
        id (str):
        window (GetMirrorRuleSummaryWindow | Unset):  Default: '1h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MirrorSummaryResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            window=window,
        )
    ).parsed
