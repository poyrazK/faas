from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_private_network_request import CreatePrivateNetworkRequest
from ...models.private_network import PrivateNetwork
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CreatePrivateNetworkRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/networks",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PrivateNetwork | Problem | None:
    if response.status_code == 201:
        response_201 = PrivateNetwork.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

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
) -> Response[PrivateNetwork | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreatePrivateNetworkRequest,
) -> Response[PrivateNetwork | Problem]:
    """Create a Gregale-owned private network.

     Creates an IPv4 RFC1918 /16-/28 address space owned by Gregale.
    The region is a Gregale placement label, not a DigitalOcean region
    identifier. Host bridge/overlay activation remains asynchronous and
    app attachments stay fail-closed until ready.

    Args:
        body (CreatePrivateNetworkRequest): POST /v1/networks body for a Gregale-owned network.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PrivateNetwork | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: CreatePrivateNetworkRequest,
) -> PrivateNetwork | Problem | None:
    """Create a Gregale-owned private network.

     Creates an IPv4 RFC1918 /16-/28 address space owned by Gregale.
    The region is a Gregale placement label, not a DigitalOcean region
    identifier. Host bridge/overlay activation remains asynchronous and
    app attachments stay fail-closed until ready.

    Args:
        body (CreatePrivateNetworkRequest): POST /v1/networks body for a Gregale-owned network.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PrivateNetwork | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreatePrivateNetworkRequest,
) -> Response[PrivateNetwork | Problem]:
    """Create a Gregale-owned private network.

     Creates an IPv4 RFC1918 /16-/28 address space owned by Gregale.
    The region is a Gregale placement label, not a DigitalOcean region
    identifier. Host bridge/overlay activation remains asynchronous and
    app attachments stay fail-closed until ready.

    Args:
        body (CreatePrivateNetworkRequest): POST /v1/networks body for a Gregale-owned network.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PrivateNetwork | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreatePrivateNetworkRequest,
) -> PrivateNetwork | Problem | None:
    """Create a Gregale-owned private network.

     Creates an IPv4 RFC1918 /16-/28 address space owned by Gregale.
    The region is a Gregale placement label, not a DigitalOcean region
    identifier. Host bridge/overlay activation remains asynchronous and
    app attachments stay fail-closed until ready.

    Args:
        body (CreatePrivateNetworkRequest): POST /v1/networks body for a Gregale-owned network.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PrivateNetwork | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
