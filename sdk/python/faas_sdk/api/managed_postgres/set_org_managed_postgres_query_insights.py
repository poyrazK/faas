from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_query_insights import ManagedPostgresQueryInsights
from ...models.problem import Problem
from ...models.set_managed_postgres_query_insights_request import SetManagedPostgresQueryInsightsRequest
from ...types import Response


def _get_kwargs(
    organization: str,
    id: str,
    *,
    body: SetManagedPostgresQueryInsightsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/query-insights".format(
            organization=quote(str(organization), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresQueryInsights | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresQueryInsights.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresQueryInsights | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresQueryInsightsRequest,
) -> Response[ManagedPostgresQueryInsights | Problem]:
    """Set native PostgreSQL query statistics collection

     Applies the collection setting at an eligible primary restart. Historical observations remain
    available until they expire.

    Args:
        organization (str):
        id (str):
        body (SetManagedPostgresQueryInsightsRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresQueryInsights | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresQueryInsightsRequest,
) -> ManagedPostgresQueryInsights | Problem | None:
    """Set native PostgreSQL query statistics collection

     Applies the collection setting at an eligible primary restart. Historical observations remain
    available until they expire.

    Args:
        organization (str):
        id (str):
        body (SetManagedPostgresQueryInsightsRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresQueryInsights | Problem
    """

    return sync_detailed(
        organization=organization,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresQueryInsightsRequest,
) -> Response[ManagedPostgresQueryInsights | Problem]:
    """Set native PostgreSQL query statistics collection

     Applies the collection setting at an eligible primary restart. Historical observations remain
    available until they expire.

    Args:
        organization (str):
        id (str):
        body (SetManagedPostgresQueryInsightsRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresQueryInsights | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresQueryInsightsRequest,
) -> ManagedPostgresQueryInsights | Problem | None:
    """Set native PostgreSQL query statistics collection

     Applies the collection setting at an eligible primary restart. Historical observations remain
    available until they expire.

    Args:
        organization (str):
        id (str):
        body (SetManagedPostgresQueryInsightsRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresQueryInsights | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
