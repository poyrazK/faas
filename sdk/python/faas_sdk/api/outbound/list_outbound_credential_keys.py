from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.outbound_credential_key_list import OutboundCredentialKeyList
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    integration: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/outbound/integrations/{integration}/credential-keys".format(
            integration=quote(str(integration), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundCredentialKeyList | Problem | None:
    if response.status_code == 200:
        response_200 = OutboundCredentialKeyList.from_dict(response.json())

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

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
) -> Response[OutboundCredentialKeyList | Problem]:
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
) -> Response[OutboundCredentialKeyList | Problem]:
    """List an integration's extra provider keys.

     Requires MFA and read scope (ADR-991). Values are write-only and never returned.

    Args:
        integration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundCredentialKeyList | Problem]
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
) -> OutboundCredentialKeyList | Problem | None:
    """List an integration's extra provider keys.

     Requires MFA and read scope (ADR-991). Values are write-only and never returned.

    Args:
        integration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundCredentialKeyList | Problem
    """

    return sync_detailed(
        integration=integration,
        client=client,
    ).parsed


async def asyncio_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OutboundCredentialKeyList | Problem]:
    """List an integration's extra provider keys.

     Requires MFA and read scope (ADR-991). Values are write-only and never returned.

    Args:
        integration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundCredentialKeyList | Problem]
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
) -> OutboundCredentialKeyList | Problem | None:
    """List an integration's extra provider keys.

     Requires MFA and read scope (ADR-991). Values are write-only and never returned.

    Args:
        integration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundCredentialKeyList | Problem
    """

    return (
        await asyncio_detailed(
            integration=integration,
            client=client,
        )
    ).parsed
