from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_outbound_credential_key_request import CreateOutboundCredentialKeyRequest
from ...models.outbound_credential_key import OutboundCredentialKey
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    integration: UUID,
    *,
    body: CreateOutboundCredentialKeyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/outbound/integrations/{integration}/credential-keys".format(
            integration=quote(str(integration), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | OutboundCredentialKey | Problem | None:
    if response.status_code == 201:
        response_201 = OutboundCredentialKey.from_dict(response.json())

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

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 429:
        response_429 = cast(Any, None)
        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | OutboundCredentialKey | Problem]:
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
    body: CreateOutboundCredentialKeyRequest,
) -> Response[Any | OutboundCredentialKey | Problem]:
    """Add an extra provider key to a customer-sealed integration.

     Requires MFA and deploy-write scope. The outbound gateway picks keys round-robin and moves a call to
    the next key when one returns 429. At most 8 extra keys.

    Args:
        integration (UUID):
        body (CreateOutboundCredentialKeyRequest): An extra provider key to seal for the outbound
            gateway, with a DNS-label label.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | OutboundCredentialKey | Problem]
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
    body: CreateOutboundCredentialKeyRequest,
) -> Any | OutboundCredentialKey | Problem | None:
    """Add an extra provider key to a customer-sealed integration.

     Requires MFA and deploy-write scope. The outbound gateway picks keys round-robin and moves a call to
    the next key when one returns 429. At most 8 extra keys.

    Args:
        integration (UUID):
        body (CreateOutboundCredentialKeyRequest): An extra provider key to seal for the outbound
            gateway, with a DNS-label label.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | OutboundCredentialKey | Problem
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
    body: CreateOutboundCredentialKeyRequest,
) -> Response[Any | OutboundCredentialKey | Problem]:
    """Add an extra provider key to a customer-sealed integration.

     Requires MFA and deploy-write scope. The outbound gateway picks keys round-robin and moves a call to
    the next key when one returns 429. At most 8 extra keys.

    Args:
        integration (UUID):
        body (CreateOutboundCredentialKeyRequest): An extra provider key to seal for the outbound
            gateway, with a DNS-label label.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | OutboundCredentialKey | Problem]
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
    body: CreateOutboundCredentialKeyRequest,
) -> Any | OutboundCredentialKey | Problem | None:
    """Add an extra provider key to a customer-sealed integration.

     Requires MFA and deploy-write scope. The outbound gateway picks keys round-robin and moves a call to
    the next key when one returns 429. At most 8 extra keys.

    Args:
        integration (UUID):
        body (CreateOutboundCredentialKeyRequest): An extra provider key to seal for the outbound
            gateway, with a DNS-label label.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | OutboundCredentialKey | Problem
    """

    return (
        await asyncio_detailed(
            integration=integration,
            client=client,
            body=body,
        )
    ).parsed
