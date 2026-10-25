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
    statement_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/usage-statements/{statement_id}".format(
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

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[PlatformTenantStatementResponse | Problem]:
    """Read one of this tenant's finalized statement revisions.

     Draft, superseded, and other tenants' statements all appear as not found.

    Args:
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantStatementResponse | Problem]
    """

    kwargs = _get_kwargs(
        statement_id=statement_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> PlatformTenantStatementResponse | Problem | None:
    """Read one of this tenant's finalized statement revisions.

     Draft, superseded, and other tenants' statements all appear as not found.

    Args:
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantStatementResponse | Problem
    """

    return sync_detailed(
        statement_id=statement_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[PlatformTenantStatementResponse | Problem]:
    """Read one of this tenant's finalized statement revisions.

     Draft, superseded, and other tenants' statements all appear as not found.

    Args:
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantStatementResponse | Problem]
    """

    kwargs = _get_kwargs(
        statement_id=statement_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> PlatformTenantStatementResponse | Problem | None:
    """Read one of this tenant's finalized statement revisions.

     Draft, superseded, and other tenants' statements all appear as not found.

    Args:
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantStatementResponse | Problem
    """

    return (
        await asyncio_detailed(
            statement_id=statement_id,
            client=client,
        )
    ).parsed
