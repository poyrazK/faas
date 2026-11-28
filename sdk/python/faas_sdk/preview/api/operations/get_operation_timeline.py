from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_timeline_response import OperationTimelineResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/operations/{id}/timeline".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationTimelineResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationTimelineResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperationTimelineResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[OperationTimelineResponse | Problem]:
    """Read one chronological history of an operation.

     Requires account read scope and MFA. Merges operation events, HTTP execution attempts, workflow step
    attempts and completion delivery attempts by time. Each source pages after its own position, so
    pages never skip or repeat a record while the operation runs. Attempt entries are snapshots at read
    time. HTTP attempts carry an instance log window for the archived log route; workflow step attempts
    do not. Platform tenants read events instead.

    Args:
        slug (str):
        id (UUID):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationTimelineResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        limit=limit,
        cursor=cursor,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> OperationTimelineResponse | Problem | None:
    """Read one chronological history of an operation.

     Requires account read scope and MFA. Merges operation events, HTTP execution attempts, workflow step
    attempts and completion delivery attempts by time. Each source pages after its own position, so
    pages never skip or repeat a record while the operation runs. Attempt entries are snapshots at read
    time. HTTP attempts carry an instance log window for the archived log route; workflow step attempts
    do not. Platform tenants read events instead.

    Args:
        slug (str):
        id (UUID):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationTimelineResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[OperationTimelineResponse | Problem]:
    """Read one chronological history of an operation.

     Requires account read scope and MFA. Merges operation events, HTTP execution attempts, workflow step
    attempts and completion delivery attempts by time. Each source pages after its own position, so
    pages never skip or repeat a record while the operation runs. Attempt entries are snapshots at read
    time. HTTP attempts carry an instance log window for the archived log route; workflow step attempts
    do not. Platform tenants read events instead.

    Args:
        slug (str):
        id (UUID):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationTimelineResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        limit=limit,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> OperationTimelineResponse | Problem | None:
    """Read one chronological history of an operation.

     Requires account read scope and MFA. Merges operation events, HTTP execution attempts, workflow step
    attempts and completion delivery attempts by time. Each source pages after its own position, so
    pages never skip or repeat a record while the operation runs. Attempt entries are snapshots at read
    time. HTTP attempts carry an instance log window for the archived log route; workflow step attempts
    do not. Platform tenants read events instead.

    Args:
        slug (str):
        id (UUID):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationTimelineResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
