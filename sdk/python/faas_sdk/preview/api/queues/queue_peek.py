from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.queue_peek_response import QueuePeekResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    limit: int | Unset = 20,
    before: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    params["before"] = before

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/queues/peek".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | QueuePeekResponse | None:
    if response.status_code == 200:
        response_200 = QueuePeekResponse.from_dict(response.json())

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | QueuePeekResponse]:
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
    before: str | Unset = UNSET,
) -> Response[Problem | QueuePeekResponse]:
    """List pending queue rows without acquiring a lease.

     Read-only peek at pending rows, oldest first. Repeated calls
    return the same rows in the same order — the underlying SQL has
    no FOR UPDATE / FOR SHARE / advisory lock, so attempts is
    never incremented and no row state changes. Cursor pagination
    matches the existing `?before=<id>` convention. NOT equivalent
    to `queues/receive` — peek never leases.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueuePeekResponse]
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
    before: str | Unset = UNSET,
) -> Problem | QueuePeekResponse | None:
    """List pending queue rows without acquiring a lease.

     Read-only peek at pending rows, oldest first. Repeated calls
    return the same rows in the same order — the underlying SQL has
    no FOR UPDATE / FOR SHARE / advisory lock, so attempts is
    never incremented and no row state changes. Cursor pagination
    matches the existing `?before=<id>` convention. NOT equivalent
    to `queues/receive` — peek never leases.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueuePeekResponse
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
    before: str | Unset = UNSET,
) -> Response[Problem | QueuePeekResponse]:
    """List pending queue rows without acquiring a lease.

     Read-only peek at pending rows, oldest first. Repeated calls
    return the same rows in the same order — the underlying SQL has
    no FOR UPDATE / FOR SHARE / advisory lock, so attempts is
    never incremented and no row state changes. Cursor pagination
    matches the existing `?before=<id>` convention. NOT equivalent
    to `queues/receive` — peek never leases.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueuePeekResponse]
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
    before: str | Unset = UNSET,
) -> Problem | QueuePeekResponse | None:
    """List pending queue rows without acquiring a lease.

     Read-only peek at pending rows, oldest first. Repeated calls
    return the same rows in the same order — the underlying SQL has
    no FOR UPDATE / FOR SHARE / advisory lock, so attempts is
    never incremented and no row state changes. Cursor pagination
    matches the existing `?before=<id>` convention. NOT equivalent
    to `queues/receive` — peek never leases.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueuePeekResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            limit=limit,
            before=before,
        )
    ).parsed
