from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_principal_receipt_response import ManagedRealtimePrincipalReceiptResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    message_id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/principals/messages/{message_id}/receipt".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            message_id=quote(str(message_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedRealtimePrincipalReceiptResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimePrincipalReceiptResponse.from_dict(response.json())

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
) -> Response[ManagedRealtimePrincipalReceiptResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedRealtimePrincipalReceiptResponse | Problem]:
    """Get principal message receipt state

    Args:
        slug (str):
        id (UUID):
        message_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimePrincipalReceiptResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        message_id=message_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedRealtimePrincipalReceiptResponse | Problem | None:
    """Get principal message receipt state

    Args:
        slug (str):
        id (UUID):
        message_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimePrincipalReceiptResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        message_id=message_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedRealtimePrincipalReceiptResponse | Problem]:
    """Get principal message receipt state

    Args:
        slug (str):
        id (UUID):
        message_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimePrincipalReceiptResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        message_id=message_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedRealtimePrincipalReceiptResponse | Problem | None:
    """Get principal message receipt state

    Args:
        slug (str):
        id (UUID):
        message_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimePrincipalReceiptResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            message_id=message_id,
            client=client,
        )
    ).parsed
