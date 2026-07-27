import datetime
from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_list_response import DeploymentListResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    limit: int | Unset = 50,
    before: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    json_before: str | Unset = UNSET
    if not isinstance(before, Unset):
        json_before = before.isoformat()
    params["before"] = json_before

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deployments",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DeploymentListResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DeploymentListResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    before: datetime.datetime | Unset = UNSET,
) -> Response[DeploymentListResponse | Problem]:
    """List deployments across all apps on the account.

     Paged backwards (newest first). `next_before` is an opaque cursor
    (RFC3339Nano of the `created_at`); pass it on the next request to
    page backwards. Empty `next_before` means end of list.

    Args:
        limit (int | Unset):  Default: 50.
        before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentListResponse | Problem]
    """

    kwargs = _get_kwargs(
        limit=limit,
        before=before,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    before: datetime.datetime | Unset = UNSET,
) -> DeploymentListResponse | Problem | None:
    """List deployments across all apps on the account.

     Paged backwards (newest first). `next_before` is an opaque cursor
    (RFC3339Nano of the `created_at`); pass it on the next request to
    page backwards. Empty `next_before` means end of list.

    Args:
        limit (int | Unset):  Default: 50.
        before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentListResponse | Problem
    """

    return sync_detailed(
        client=client,
        limit=limit,
        before=before,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    before: datetime.datetime | Unset = UNSET,
) -> Response[DeploymentListResponse | Problem]:
    """List deployments across all apps on the account.

     Paged backwards (newest first). `next_before` is an opaque cursor
    (RFC3339Nano of the `created_at`); pass it on the next request to
    page backwards. Empty `next_before` means end of list.

    Args:
        limit (int | Unset):  Default: 50.
        before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentListResponse | Problem]
    """

    kwargs = _get_kwargs(
        limit=limit,
        before=before,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    before: datetime.datetime | Unset = UNSET,
) -> DeploymentListResponse | Problem | None:
    """List deployments across all apps on the account.

     Paged backwards (newest first). `next_before` is an opaque cursor
    (RFC3339Nano of the `created_at`); pass it on the next request to
    page backwards. Empty `next_before` means end of list.

    Args:
        limit (int | Unset):  Default: 50.
        before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentListResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            limit=limit,
            before=before,
        )
    ).parsed
