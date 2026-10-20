from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.private_network_members_response import PrivateNetworkMembersResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/networks/{id}/members".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PrivateNetworkMembersResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PrivateNetworkMembersResponse.from_dict(response.json())

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
) -> Response[PrivateNetworkMembersResponse | Problem]:
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
) -> Response[PrivateNetworkMembersResponse | Problem]:
    """List members and address capacity for a private network.

     Returns the account-scoped stable address reservations for this
    Gregale-owned network. Capacity excludes the network address, gateway,
    and broadcast address; this endpoint is inventory only and does not
    probe workloads or call DigitalOcean APIs.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PrivateNetworkMembersResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> PrivateNetworkMembersResponse | Problem | None:
    """List members and address capacity for a private network.

     Returns the account-scoped stable address reservations for this
    Gregale-owned network. Capacity excludes the network address, gateway,
    and broadcast address; this endpoint is inventory only and does not
    probe workloads or call DigitalOcean APIs.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PrivateNetworkMembersResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[PrivateNetworkMembersResponse | Problem]:
    """List members and address capacity for a private network.

     Returns the account-scoped stable address reservations for this
    Gregale-owned network. Capacity excludes the network address, gateway,
    and broadcast address; this endpoint is inventory only and does not
    probe workloads or call DigitalOcean APIs.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PrivateNetworkMembersResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> PrivateNetworkMembersResponse | Problem | None:
    """List members and address capacity for a private network.

     Returns the account-scoped stable address reservations for this
    Gregale-owned network. Capacity excludes the network address, gateway,
    and broadcast address; this endpoint is inventory only and does not
    probe workloads or call DigitalOcean APIs.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PrivateNetworkMembersResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
