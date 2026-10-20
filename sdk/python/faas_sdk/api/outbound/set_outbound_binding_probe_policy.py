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
    *,
    body: OutboundBindingProbePolicy,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/outbound/integrations/{integration}/probe-policy".format(
            integration=quote(str(integration), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
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
    body: OutboundBindingProbePolicy,
) -> Response[OutboundBindingProbePolicy | Problem]:
    """Configure an outbound binding probe

     Declare a GET or HEAD path safe to probe and its expected 2xx status. The path must fit the
    customer-owned integration's route policy. Configuration sends no provider requests. Requires
    deploy-write access and MFA.

    Args:
        integration (UUID):
        body (OutboundBindingProbePolicy): Explicit provider endpoint declared safe to probe using
            managed outbound admission. Queries, redirects and unsuccessful expected statuses are
            unsupported.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundBindingProbePolicy | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: OutboundBindingProbePolicy,
) -> OutboundBindingProbePolicy | Problem | None:
    """Configure an outbound binding probe

     Declare a GET or HEAD path safe to probe and its expected 2xx status. The path must fit the
    customer-owned integration's route policy. Configuration sends no provider requests. Requires
    deploy-write access and MFA.

    Args:
        integration (UUID):
        body (OutboundBindingProbePolicy): Explicit provider endpoint declared safe to probe using
            managed outbound admission. Queries, redirects and unsuccessful expected statuses are
            unsupported.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundBindingProbePolicy | Problem
    """

    return sync_detailed(
        integration=integration,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: OutboundBindingProbePolicy,
) -> Response[OutboundBindingProbePolicy | Problem]:
    """Configure an outbound binding probe

     Declare a GET or HEAD path safe to probe and its expected 2xx status. The path must fit the
    customer-owned integration's route policy. Configuration sends no provider requests. Requires
    deploy-write access and MFA.

    Args:
        integration (UUID):
        body (OutboundBindingProbePolicy): Explicit provider endpoint declared safe to probe using
            managed outbound admission. Queries, redirects and unsuccessful expected statuses are
            unsupported.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundBindingProbePolicy | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: OutboundBindingProbePolicy,
) -> OutboundBindingProbePolicy | Problem | None:
    """Configure an outbound binding probe

     Declare a GET or HEAD path safe to probe and its expected 2xx status. The path must fit the
    customer-owned integration's route policy. Configuration sends no provider requests. Requires
    deploy-write access and MFA.

    Args:
        integration (UUID):
        body (OutboundBindingProbePolicy): Explicit provider endpoint declared safe to probe using
            managed outbound admission. Queries, redirects and unsuccessful expected statuses are
            unsupported.

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
            body=body,
        )
    ).parsed
