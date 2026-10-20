from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_inbox_response import ManagedRealtimeInboxResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    principal: str,
    after: int | Unset = UNSET,
    limit: int | Unset = 100,
    consumer: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["principal"] = principal

    params["after"] = after

    params["limit"] = limit

    params["consumer"] = consumer

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedRealtimeInboxResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeInboxResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedRealtimeInboxResponse | Problem]:
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
    principal: str,
    after: int | Unset = UNSET,
    limit: int | Unset = 100,
    consumer: str | Unset = UNSET,
) -> Response[ManagedRealtimeInboxResponse | Problem]:
    """Read retained principal inbox messages

    Args:
        slug (str):
        id (UUID):
        principal (str):
        after (int | Unset):
        limit (int | Unset):  Default: 100.
        consumer (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeInboxResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        principal=principal,
        after=after,
        limit=limit,
        consumer=consumer,
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
    principal: str,
    after: int | Unset = UNSET,
    limit: int | Unset = 100,
    consumer: str | Unset = UNSET,
) -> ManagedRealtimeInboxResponse | Problem | None:
    """Read retained principal inbox messages

    Args:
        slug (str):
        id (UUID):
        principal (str):
        after (int | Unset):
        limit (int | Unset):  Default: 100.
        consumer (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeInboxResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        principal=principal,
        after=after,
        limit=limit,
        consumer=consumer,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    principal: str,
    after: int | Unset = UNSET,
    limit: int | Unset = 100,
    consumer: str | Unset = UNSET,
) -> Response[ManagedRealtimeInboxResponse | Problem]:
    """Read retained principal inbox messages

    Args:
        slug (str):
        id (UUID):
        principal (str):
        after (int | Unset):
        limit (int | Unset):  Default: 100.
        consumer (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeInboxResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        principal=principal,
        after=after,
        limit=limit,
        consumer=consumer,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    principal: str,
    after: int | Unset = UNSET,
    limit: int | Unset = 100,
    consumer: str | Unset = UNSET,
) -> ManagedRealtimeInboxResponse | Problem | None:
    """Read retained principal inbox messages

    Args:
        slug (str):
        id (UUID):
        principal (str):
        after (int | Unset):
        limit (int | Unset):  Default: 100.
        consumer (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeInboxResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            principal=principal,
            after=after,
            limit=limit,
            consumer=consumer,
        )
    ).parsed
