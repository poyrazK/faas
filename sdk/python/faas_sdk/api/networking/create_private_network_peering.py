from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_private_network_peering_request import CreatePrivateNetworkPeeringRequest
from ...models.private_network_peering import PrivateNetworkPeering
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: CreatePrivateNetworkPeeringRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/networks/{id}/peerings".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PrivateNetworkPeering | Problem | None:
    if response.status_code == 202:
        response_202 = PrivateNetworkPeering.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
    *,
    client: AuthenticatedClient | Client,
    body: CreatePrivateNetworkPeeringRequest,
) -> Response[PrivateNetworkPeering | Problem]:
    """Request peering with another private network.

     Creates a pending, symmetric peering between two Gregale-owned
    networks in the same account and region. Networks must have
    non-overlapping IPv4 CIDRs. Route activation is asynchronous and
    remains fail-closed until the node fabric converges.

    Args:
        id (str):
        body (CreatePrivateNetworkPeeringRequest): POST /v1/networks/{id}/peerings body.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PrivateNetworkPeering | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreatePrivateNetworkPeeringRequest,
) -> PrivateNetworkPeering | Problem | None:
    """Request peering with another private network.

     Creates a pending, symmetric peering between two Gregale-owned
    networks in the same account and region. Networks must have
    non-overlapping IPv4 CIDRs. Route activation is asynchronous and
    remains fail-closed until the node fabric converges.

    Args:
        id (str):
        body (CreatePrivateNetworkPeeringRequest): POST /v1/networks/{id}/peerings body.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PrivateNetworkPeering | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreatePrivateNetworkPeeringRequest,
) -> Response[PrivateNetworkPeering | Problem]:
    """Request peering with another private network.

     Creates a pending, symmetric peering between two Gregale-owned
    networks in the same account and region. Networks must have
    non-overlapping IPv4 CIDRs. Route activation is asynchronous and
    remains fail-closed until the node fabric converges.

    Args:
        id (str):
        body (CreatePrivateNetworkPeeringRequest): POST /v1/networks/{id}/peerings body.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PrivateNetworkPeering | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreatePrivateNetworkPeeringRequest,
) -> PrivateNetworkPeering | Problem | None:
    """Request peering with another private network.

     Creates a pending, symmetric peering between two Gregale-owned
    networks in the same account and region. Networks must have
    non-overlapping IPv4 CIDRs. Route activation is asynchronous and
    remains fail-closed until the node fabric converges.

    Args:
        id (str):
        body (CreatePrivateNetworkPeeringRequest): POST /v1/networks/{id}/peerings body.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PrivateNetworkPeering | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
