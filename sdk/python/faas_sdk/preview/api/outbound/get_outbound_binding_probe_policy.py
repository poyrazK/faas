from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.outbound_binding_probe_policy import OutboundBindingProbePolicy
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    integration: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/outbound/integrations/{integration}/probe-policy".format(
            integration=quote(str(integration), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundBindingProbePolicy | Problem:
    if response.status_code == 200:
        response_200 = OutboundBindingProbePolicy.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OutboundBindingProbePolicy | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OutboundBindingProbePolicy | Problem]:
    """Read an explicitly configured outbound probe policy

     Read a customer-owned integration's safe-method probe configuration. This does not send traffic to a
    provider.

    Args:
        integration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundBindingProbePolicy | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> OutboundBindingProbePolicy | Problem | None:
    """Read an explicitly configured outbound probe policy

     Read a customer-owned integration's safe-method probe configuration. This does not send traffic to a
    provider.

    Args:
        integration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundBindingProbePolicy | Problem
    """

    return sync_detailed(
        integration=integration,
        client=client,
    ).parsed


async def asyncio_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OutboundBindingProbePolicy | Problem]:
    """Read an explicitly configured outbound probe policy

     Read a customer-owned integration's safe-method probe configuration. This does not send traffic to a
    provider.

    Args:
        integration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundBindingProbePolicy | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> OutboundBindingProbePolicy | Problem | None:
    """Read an explicitly configured outbound probe policy

     Read a customer-owned integration's safe-method probe configuration. This does not send traffic to a
    provider.

    Args:
        integration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundBindingProbePolicy | Problem
    """

    return (
        await asyncio_detailed(
            integration=integration,
            client=client,
        )
    ).parsed
