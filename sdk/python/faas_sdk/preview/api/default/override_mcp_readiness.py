from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_readiness_override import MCPReadinessOverride
from ...models.mcp_readiness_override_request import MCPReadinessOverrideRequest
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: MCPReadinessOverrideRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/deployments/{id}/mcp-readiness-overrides".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPReadinessOverride | None:
    if response.status_code == 201:
        response_201 = MCPReadinessOverride.from_dict(response.json())

        return response_201

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | MCPReadinessOverride]:
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
    body: MCPReadinessOverrideRequest,
) -> Response[Any | MCPReadinessOverride]:
    """Create an audited five-minute readiness override for one traffic increase

     Requires admin scope and applicable MFA. Bound to the current artifact and policy revision. Other
    admission guards remain enforced. Consumed atomically with a successful traffic mutation.

    Args:
        id (str):
        body (MCPReadinessOverrideRequest): Request to record an audited readiness override for a
            deployment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessOverride]
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
    body: MCPReadinessOverrideRequest,
) -> Any | MCPReadinessOverride | None:
    """Create an audited five-minute readiness override for one traffic increase

     Requires admin scope and applicable MFA. Bound to the current artifact and policy revision. Other
    admission guards remain enforced. Consumed atomically with a successful traffic mutation.

    Args:
        id (str):
        body (MCPReadinessOverrideRequest): Request to record an audited readiness override for a
            deployment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessOverride
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
    body: MCPReadinessOverrideRequest,
) -> Response[Any | MCPReadinessOverride]:
    """Create an audited five-minute readiness override for one traffic increase

     Requires admin scope and applicable MFA. Bound to the current artifact and policy revision. Other
    admission guards remain enforced. Consumed atomically with a successful traffic mutation.

    Args:
        id (str):
        body (MCPReadinessOverrideRequest): Request to record an audited readiness override for a
            deployment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessOverride]
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
    body: MCPReadinessOverrideRequest,
) -> Any | MCPReadinessOverride | None:
    """Create an audited five-minute readiness override for one traffic increase

     Requires admin scope and applicable MFA. Bound to the current artifact and policy revision. Other
    admission guards remain enforced. Consumed atomically with a successful traffic mutation.

    Args:
        id (str):
        body (MCPReadinessOverrideRequest): Request to record an audited readiness override for a
            deployment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessOverride
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
