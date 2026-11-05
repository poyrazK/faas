from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.private_network_peering import PrivateNetworkPeering
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    peer_id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/networks/{id}/peerings/{peer_id}".format(
            id=quote(str(id), safe=""),
            peer_id=quote(str(peer_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PrivateNetworkPeering | Problem | None:
    if response.status_code == 200:
        response_200 = PrivateNetworkPeering.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[PrivateNetworkPeering | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    peer_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[PrivateNetworkPeering | Problem]:
    """Read a private-network peering.

    Args:
        id (str):
        peer_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PrivateNetworkPeering | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        peer_id=peer_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    peer_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> PrivateNetworkPeering | Problem | None:
    """Read a private-network peering.

    Args:
        id (str):
        peer_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PrivateNetworkPeering | Problem
    """

    return sync_detailed(
        id=id,
        peer_id=peer_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    peer_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[PrivateNetworkPeering | Problem]:
    """Read a private-network peering.

    Args:
        id (str):
        peer_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PrivateNetworkPeering | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        peer_id=peer_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    peer_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> PrivateNetworkPeering | Problem | None:
    """Read a private-network peering.

    Args:
        id (str):
        peer_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PrivateNetworkPeering | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            peer_id=peer_id,
            client=client,
        )
    ).parsed
