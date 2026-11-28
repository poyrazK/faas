from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.private_network import PrivateNetwork
from ...models.problem import Problem
from ...models.update_private_network_policy_request import UpdatePrivateNetworkPolicyRequest
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: UpdatePrivateNetworkPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/networks/{id}/policy".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PrivateNetwork | Problem | None:
    if response.status_code == 200:
        response_200 = PrivateNetwork.from_dict(response.json())

        return response_200

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
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdatePrivateNetworkPolicyRequest,
) -> Response[PrivateNetwork | Problem]:
    """Replace a private network's reusable firewall policy.

     Replaces the network-level IPv4 CIDR allowlist and optional
    protocol/port rules used by every attached workload. Empty lists
    preserve the legacy allow-all behavior; non-empty rules are enforced
    fail-closed. App-level policy may further restrict destinations but
    cannot broaden the network baseline.

    Args:
        id (str):
        body (UpdatePrivateNetworkPolicyRequest): PUT /v1/networks/{id}/policy body.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PrivateNetwork | Problem]
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
    body: UpdatePrivateNetworkPolicyRequest,
) -> PrivateNetwork | Problem | None:
    """Replace a private network's reusable firewall policy.

     Replaces the network-level IPv4 CIDR allowlist and optional
    protocol/port rules used by every attached workload. Empty lists
    preserve the legacy allow-all behavior; non-empty rules are enforced
    fail-closed. App-level policy may further restrict destinations but
    cannot broaden the network baseline.

    Args:
        id (str):
        body (UpdatePrivateNetworkPolicyRequest): PUT /v1/networks/{id}/policy body.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PrivateNetwork | Problem
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
    body: UpdatePrivateNetworkPolicyRequest,
) -> Response[PrivateNetwork | Problem]:
    """Replace a private network's reusable firewall policy.

     Replaces the network-level IPv4 CIDR allowlist and optional
    protocol/port rules used by every attached workload. Empty lists
    preserve the legacy allow-all behavior; non-empty rules are enforced
    fail-closed. App-level policy may further restrict destinations but
    cannot broaden the network baseline.

    Args:
        id (str):
        body (UpdatePrivateNetworkPolicyRequest): PUT /v1/networks/{id}/policy body.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PrivateNetwork | Problem]
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
    body: UpdatePrivateNetworkPolicyRequest,
) -> PrivateNetwork | Problem | None:
    """Replace a private network's reusable firewall policy.

     Replaces the network-level IPv4 CIDR allowlist and optional
    protocol/port rules used by every attached workload. Empty lists
    preserve the legacy allow-all behavior; non-empty rules are enforced
    fail-closed. App-level policy may further restrict destinations but
    cannot broaden the network baseline.

    Args:
        id (str):
        body (UpdatePrivateNetworkPolicyRequest): PUT /v1/networks/{id}/policy body.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PrivateNetwork | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
