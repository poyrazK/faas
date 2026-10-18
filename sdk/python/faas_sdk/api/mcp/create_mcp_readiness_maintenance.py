from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_mcp_readiness_maintenance_request import CreateMCPReadinessMaintenanceRequest
from ...models.mcp_readiness_maintenance_window import MCPReadinessMaintenanceWindow
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateMCPReadinessMaintenanceRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/mcp-readiness-maintenance".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPReadinessMaintenanceWindow | None:
    if response.status_code == 201:
        response_201 = MCPReadinessMaintenanceWindow.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | MCPReadinessMaintenanceWindow]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateMCPReadinessMaintenanceRequest,
) -> Response[Any | MCPReadinessMaintenanceWindow]:
    """Schedule maintenance or temporarily silence readiness notifications

     Admin scope and MFA required. Monitoring and incident recording continue. Windows match exact
    environments and end automatically. An omitted start begins now; duration is at most 720 hours.
    Notifications already claimed may complete.

    Args:
        slug (str):
        body (CreateMCPReadinessMaintenanceRequest): Exact-environment notification silence with a
            finite time range and audit reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessMaintenanceWindow]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateMCPReadinessMaintenanceRequest,
) -> Any | MCPReadinessMaintenanceWindow | None:
    """Schedule maintenance or temporarily silence readiness notifications

     Admin scope and MFA required. Monitoring and incident recording continue. Windows match exact
    environments and end automatically. An omitted start begins now; duration is at most 720 hours.
    Notifications already claimed may complete.

    Args:
        slug (str):
        body (CreateMCPReadinessMaintenanceRequest): Exact-environment notification silence with a
            finite time range and audit reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessMaintenanceWindow
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateMCPReadinessMaintenanceRequest,
) -> Response[Any | MCPReadinessMaintenanceWindow]:
    """Schedule maintenance or temporarily silence readiness notifications

     Admin scope and MFA required. Monitoring and incident recording continue. Windows match exact
    environments and end automatically. An omitted start begins now; duration is at most 720 hours.
    Notifications already claimed may complete.

    Args:
        slug (str):
        body (CreateMCPReadinessMaintenanceRequest): Exact-environment notification silence with a
            finite time range and audit reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessMaintenanceWindow]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateMCPReadinessMaintenanceRequest,
) -> Any | MCPReadinessMaintenanceWindow | None:
    """Schedule maintenance or temporarily silence readiness notifications

     Admin scope and MFA required. Monitoring and incident recording continue. Windows match exact
    environments and end automatically. An omitted start begins now; duration is at most 720 hours.
    Notifications already claimed may complete.

    Args:
        slug (str):
        body (CreateMCPReadinessMaintenanceRequest): Exact-environment notification silence with a
            finite time range and audit reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessMaintenanceWindow
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
