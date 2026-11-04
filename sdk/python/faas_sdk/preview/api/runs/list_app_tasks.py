from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_task_list_response import AppTaskListResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    params["offset"] = offset

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/tasks".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppTaskListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppTaskListResponse.from_dict(response.json())

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

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppTaskListResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
) -> Response[AppTaskListResponse | Problem]:
    """List deployment-attached tasks for an app.

     Returns newest-first one-off command receipts for the app. Task rows
    remain pinned to the immutable deployment selected at admission.
    Scheduler leases and artifact storage identifiers are never returned.

    Args:
        slug (str):
        limit (int | Unset):  Default: 50.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppTaskListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        limit=limit,
        offset=offset,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
) -> AppTaskListResponse | Problem | None:
    """List deployment-attached tasks for an app.

     Returns newest-first one-off command receipts for the app. Task rows
    remain pinned to the immutable deployment selected at admission.
    Scheduler leases and artifact storage identifiers are never returned.

    Args:
        slug (str):
        limit (int | Unset):  Default: 50.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppTaskListResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        limit=limit,
        offset=offset,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
) -> Response[AppTaskListResponse | Problem]:
    """List deployment-attached tasks for an app.

     Returns newest-first one-off command receipts for the app. Task rows
    remain pinned to the immutable deployment selected at admission.
    Scheduler leases and artifact storage identifiers are never returned.

    Args:
        slug (str):
        limit (int | Unset):  Default: 50.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppTaskListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        limit=limit,
        offset=offset,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
) -> AppTaskListResponse | Problem | None:
    """List deployment-attached tasks for an app.

     Returns newest-first one-off command receipts for the app. Task rows
    remain pinned to the immutable deployment selected at admission.
    Scheduler leases and artifact storage identifiers are never returned.

    Args:
        slug (str):
        limit (int | Unset):  Default: 50.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppTaskListResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            limit=limit,
            offset=offset,
        )
    ).parsed
