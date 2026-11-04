from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_statement_response import PlatformTenantStatementResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    statement_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/platform-tenants/{id}/usage-statements/{statement_id}".format(
            id=quote(str(id), safe=""),
            statement_id=quote(str(statement_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantStatementResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantStatementResponse.from_dict(response.json())

        return response_200

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantStatementResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[PlatformTenantStatementResponse | Problem]:
    """Read an immutable cross-app statement revision.

    Args:
        id (UUID):
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantStatementResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        statement_id=statement_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> PlatformTenantStatementResponse | Problem | None:
    """Read an immutable cross-app statement revision.

    Args:
        id (UUID):
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantStatementResponse | Problem
    """

    return sync_detailed(
        id=id,
        statement_id=statement_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[PlatformTenantStatementResponse | Problem]:
    """Read an immutable cross-app statement revision.

    Args:
        id (UUID):
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantStatementResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        statement_id=statement_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> PlatformTenantStatementResponse | Problem | None:
    """Read an immutable cross-app statement revision.

    Args:
        id (UUID):
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantStatementResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            statement_id=statement_id,
            client=client,
        )
    ).parsed
