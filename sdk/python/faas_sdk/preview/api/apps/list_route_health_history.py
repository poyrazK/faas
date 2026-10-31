from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_health_history_page import RouteHealthHistoryPage
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    deployment: UUID,
    *,
    limit: int | Unset = 5,
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
        "url": "/v1/apps/{slug}/route-health/deployments/{deployment}/history".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteHealthHistoryPage | None:
    if response.status_code == 200:
        response_200 = RouteHealthHistoryPage.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RouteHealthHistoryPage]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 5,
    before: UUID | Unset = UNSET,
) -> Response[Problem | RouteHealthHistoryPage]:
    """Explain saved canary route health decisions with an immutable timeline.

     Requires apps:read or admin and completed MFA. Available after plan downgrade and rollout
    completion. Only real advance evaluations with selected routes create snapshots; reads never create
    history. Identical retries reuse the original decision. Retains the newest 100 entries within 4 MiB
    per deployment, with each encoded entry bounded to 64 KiB. Pages are newest first by checked_at and
    id. A missing, foreign or pruned cursor returns 404. Historical evidence does not establish current
    health or continuous incident duration.

    Args:
        slug (str):
        deployment (UUID):
        limit (int | Unset):  Default: 5.
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteHealthHistoryPage]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        limit=limit,
        before=before,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 5,
    before: UUID | Unset = UNSET,
) -> Problem | RouteHealthHistoryPage | None:
    """Explain saved canary route health decisions with an immutable timeline.

     Requires apps:read or admin and completed MFA. Available after plan downgrade and rollout
    completion. Only real advance evaluations with selected routes create snapshots; reads never create
    history. Identical retries reuse the original decision. Retains the newest 100 entries within 4 MiB
    per deployment, with each encoded entry bounded to 64 KiB. Pages are newest first by checked_at and
    id. A missing, foreign or pruned cursor returns 404. Historical evidence does not establish current
    health or continuous incident duration.

    Args:
        slug (str):
        deployment (UUID):
        limit (int | Unset):  Default: 5.
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteHealthHistoryPage
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        client=client,
        limit=limit,
        before=before,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 5,
    before: UUID | Unset = UNSET,
) -> Response[Problem | RouteHealthHistoryPage]:
    """Explain saved canary route health decisions with an immutable timeline.

     Requires apps:read or admin and completed MFA. Available after plan downgrade and rollout
    completion. Only real advance evaluations with selected routes create snapshots; reads never create
    history. Identical retries reuse the original decision. Retains the newest 100 entries within 4 MiB
    per deployment, with each encoded entry bounded to 64 KiB. Pages are newest first by checked_at and
    id. A missing, foreign or pruned cursor returns 404. Historical evidence does not establish current
    health or continuous incident duration.

    Args:
        slug (str):
        deployment (UUID):
        limit (int | Unset):  Default: 5.
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteHealthHistoryPage]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        limit=limit,
        before=before,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 5,
    before: UUID | Unset = UNSET,
) -> Problem | RouteHealthHistoryPage | None:
    """Explain saved canary route health decisions with an immutable timeline.

     Requires apps:read or admin and completed MFA. Available after plan downgrade and rollout
    completion. Only real advance evaluations with selected routes create snapshots; reads never create
    history. Identical retries reuse the original decision. Retains the newest 100 entries within 4 MiB
    per deployment, with each encoded entry bounded to 64 KiB. Pages are newest first by checked_at and
    id. A missing, foreign or pruned cursor returns 404. Historical evidence does not establish current
    health or continuous incident duration.

    Args:
        slug (str):
        deployment (UUID):
        limit (int | Unset):  Default: 5.
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteHealthHistoryPage
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            client=client,
            limit=limit,
            before=before,
        )
    ).parsed
