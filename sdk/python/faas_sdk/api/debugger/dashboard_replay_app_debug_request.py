from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.dashboard_replay_app_debug_request_body import DashboardReplayAppDebugRequestBody
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    req_id: UUID,
    *,
    body: DashboardReplayAppDebugRequestBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/dashboard/apps/{slug}/debug/requests/{req_id}/replay".format(
            slug=quote(str(slug), safe=""),
            req_id=quote(str(req_id), safe=""),
        ),
    }

    _kwargs["data"] = body.to_dict()
    headers["Content-Type"] = "application/x-www-form-urlencoded"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 303:
        response_303 = cast(Any, None)
        return response_303

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    req_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DashboardReplayAppDebugRequestBody,
) -> Response[Any | Problem]:
    """Queue a debugger replay from the dashboard.

     Accepts the server-rendered dashboard form payload and queues the
    same metadata-only replay as POST /v1/apps/{slug}/debug/requests/{req_id}/replay.
    The response redirects to the request detail with a replay_id query
    parameter; request bodies, credentials, and customer headers are
    never replayed.

    Args:
        slug (str):
        req_id (UUID):
        body (DashboardReplayAppDebugRequestBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        req_id=req_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    req_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DashboardReplayAppDebugRequestBody,
) -> Any | Problem | None:
    """Queue a debugger replay from the dashboard.

     Accepts the server-rendered dashboard form payload and queues the
    same metadata-only replay as POST /v1/apps/{slug}/debug/requests/{req_id}/replay.
    The response redirects to the request detail with a replay_id query
    parameter; request bodies, credentials, and customer headers are
    never replayed.

    Args:
        slug (str):
        req_id (UUID):
        body (DashboardReplayAppDebugRequestBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        req_id=req_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    req_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DashboardReplayAppDebugRequestBody,
) -> Response[Any | Problem]:
    """Queue a debugger replay from the dashboard.

     Accepts the server-rendered dashboard form payload and queues the
    same metadata-only replay as POST /v1/apps/{slug}/debug/requests/{req_id}/replay.
    The response redirects to the request detail with a replay_id query
    parameter; request bodies, credentials, and customer headers are
    never replayed.

    Args:
        slug (str):
        req_id (UUID):
        body (DashboardReplayAppDebugRequestBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        req_id=req_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    req_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DashboardReplayAppDebugRequestBody,
) -> Any | Problem | None:
    """Queue a debugger replay from the dashboard.

     Accepts the server-rendered dashboard form payload and queues the
    same metadata-only replay as POST /v1/apps/{slug}/debug/requests/{req_id}/replay.
    The response redirects to the request detail with a replay_id query
    parameter; request bodies, credentials, and customer headers are
    never replayed.

    Args:
        slug (str):
        req_id (UUID):
        body (DashboardReplayAppDebugRequestBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            req_id=req_id,
            client=client,
            body=body,
        )
    ).parsed
