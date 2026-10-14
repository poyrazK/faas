from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcpo_auth_retirement import MCPOAuthRetirement
from ...models.mcpo_auth_retirement_action import MCPOAuthRetirementAction
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: MCPOAuthRetirementAction,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/mcp-oauth-retirements/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPOAuthRetirement | None:
    if response.status_code == 200:
        response_200 = MCPOAuthRetirement.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

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
) -> Response[Any | MCPOAuthRetirement]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: MCPOAuthRetirementAction,
) -> Response[Any | MCPOAuthRetirement]:
    """Retry retirement or record provider-side manual revocation; administrator access and MFA policy
    apply

    Args:
        id (UUID):
        body (MCPOAuthRetirementAction): Explicit retirement recovery; allowing active grant
            revocation may invalidate a replacement connection.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPOAuthRetirement]
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
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: MCPOAuthRetirementAction,
) -> Any | MCPOAuthRetirement | None:
    """Retry retirement or record provider-side manual revocation; administrator access and MFA policy
    apply

    Args:
        id (UUID):
        body (MCPOAuthRetirementAction): Explicit retirement recovery; allowing active grant
            revocation may invalidate a replacement connection.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPOAuthRetirement
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: MCPOAuthRetirementAction,
) -> Response[Any | MCPOAuthRetirement]:
    """Retry retirement or record provider-side manual revocation; administrator access and MFA policy
    apply

    Args:
        id (UUID):
        body (MCPOAuthRetirementAction): Explicit retirement recovery; allowing active grant
            revocation may invalidate a replacement connection.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPOAuthRetirement]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: MCPOAuthRetirementAction,
) -> Any | MCPOAuthRetirement | None:
    """Retry retirement or record provider-side manual revocation; administrator access and MFA policy
    apply

    Args:
        id (UUID):
        body (MCPOAuthRetirementAction): Explicit retirement recovery; allowing active grant
            revocation may invalidate a replacement connection.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPOAuthRetirement
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
