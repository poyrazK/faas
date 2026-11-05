from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.outbound_binding_usage_response import OutboundBindingUsageResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    integration: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/outbound-bindings/{integration}/usage".format(
            slug=quote(str(slug), safe=""),
            integration=quote(str(integration), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundBindingUsageResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OutboundBindingUsageResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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
) -> Response[OutboundBindingUsageResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OutboundBindingUsageResponse | Problem]:
    """Read an app binding's current UTC-day outbound request usage.

     Requires MFA and read-surface scope. Reports UTC-day admissions for this app-to-integration binding,
    including provider calls that later fail; the returned timestamp marks its reset.

    Args:
        slug (str):
        integration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundBindingUsageResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        integration=integration,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> OutboundBindingUsageResponse | Problem | None:
    """Read an app binding's current UTC-day outbound request usage.

     Requires MFA and read-surface scope. Reports UTC-day admissions for this app-to-integration binding,
    including provider calls that later fail; the returned timestamp marks its reset.

    Args:
        slug (str):
        integration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundBindingUsageResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        integration=integration,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OutboundBindingUsageResponse | Problem]:
    """Read an app binding's current UTC-day outbound request usage.

     Requires MFA and read-surface scope. Reports UTC-day admissions for this app-to-integration binding,
    including provider calls that later fail; the returned timestamp marks its reset.

    Args:
        slug (str):
        integration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundBindingUsageResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        integration=integration,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> OutboundBindingUsageResponse | Problem | None:
    """Read an app binding's current UTC-day outbound request usage.

     Requires MFA and read-surface scope. Reports UTC-day admissions for this app-to-integration binding,
    including provider calls that later fail; the returned timestamp marks its reset.

    Args:
        slug (str):
        integration (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundBindingUsageResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            integration=integration,
            client=client,
        )
    ).parsed
