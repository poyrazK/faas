from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.stream_deployment_logs_follow import StreamDeploymentLogsFollow
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    before_seq: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    follow: StreamDeploymentLogsFollow | Unset = 0,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["before_seq"] = before_seq

    params["limit"] = limit

    json_follow: int | Unset = UNSET
    if not isinstance(follow, Unset):
        json_follow = follow

    params["follow"] = json_follow

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deployments/{id}/logs".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 200:
        response_200 = cast(Any, None)
        return response_200

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    before_seq: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    follow: StreamDeploymentLogsFollow | Unset = 0,
) -> Response[Any | Problem]:
    """Stream build logs (SSE).

     Server-Sent Events stream of build logs. `follow=1` holds the
    connection open until the build completes.

    Args:
        id (str):
        before_seq (int | Unset):
        limit (int | Unset):
        follow (StreamDeploymentLogsFollow | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        before_seq=before_seq,
        limit=limit,
        follow=follow,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    before_seq: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    follow: StreamDeploymentLogsFollow | Unset = 0,
) -> Any | Problem | None:
    """Stream build logs (SSE).

     Server-Sent Events stream of build logs. `follow=1` holds the
    connection open until the build completes.

    Args:
        id (str):
        before_seq (int | Unset):
        limit (int | Unset):
        follow (StreamDeploymentLogsFollow | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        before_seq=before_seq,
        limit=limit,
        follow=follow,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    before_seq: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    follow: StreamDeploymentLogsFollow | Unset = 0,
) -> Response[Any | Problem]:
    """Stream build logs (SSE).

     Server-Sent Events stream of build logs. `follow=1` holds the
    connection open until the build completes.

    Args:
        id (str):
        before_seq (int | Unset):
        limit (int | Unset):
        follow (StreamDeploymentLogsFollow | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        before_seq=before_seq,
        limit=limit,
        follow=follow,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    before_seq: int | Unset = UNSET,
    limit: int | Unset = UNSET,
    follow: StreamDeploymentLogsFollow | Unset = 0,
) -> Any | Problem | None:
    """Stream build logs (SSE).

     Server-Sent Events stream of build logs. `follow=1` holds the
    connection open until the build completes.

    Args:
        id (str):
        before_seq (int | Unset):
        limit (int | Unset):
        follow (StreamDeploymentLogsFollow | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            before_seq=before_seq,
            limit=limit,
            follow=follow,
        )
    ).parsed
