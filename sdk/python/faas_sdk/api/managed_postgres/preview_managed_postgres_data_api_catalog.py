from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_query_response import ManagedPostgresQueryResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/postgres/databases/{id}/data-api-catalog-preview".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresQueryResponse | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresQueryResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresQueryResponse | Problem]:
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
) -> Response[ManagedPostgresQueryResponse | Problem]:
    """Inspect public-schema metadata for a Data API connection.

     Requires data:read (or admin) and MFA. Runs a fixed catalog-only
    statement as the provider-managed read-only query identity, in a
    bounded read-only transaction. No request body, customer table rows,
    SQL expressions or credentials are returned. The query identity may
    be created or refreshed and a suspended database can wake. Results
    describe that identity, not a future Data API binding. At most 1000
    rows and 1 MiB of values are returned; truncated marks partial results.
    Cache-Control no-store.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresQueryResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresQueryResponse | Problem | None:
    """Inspect public-schema metadata for a Data API connection.

     Requires data:read (or admin) and MFA. Runs a fixed catalog-only
    statement as the provider-managed read-only query identity, in a
    bounded read-only transaction. No request body, customer table rows,
    SQL expressions or credentials are returned. The query identity may
    be created or refreshed and a suspended database can wake. Results
    describe that identity, not a future Data API binding. At most 1000
    rows and 1 MiB of values are returned; truncated marks partial results.
    Cache-Control no-store.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresQueryResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresQueryResponse | Problem]:
    """Inspect public-schema metadata for a Data API connection.

     Requires data:read (or admin) and MFA. Runs a fixed catalog-only
    statement as the provider-managed read-only query identity, in a
    bounded read-only transaction. No request body, customer table rows,
    SQL expressions or credentials are returned. The query identity may
    be created or refreshed and a suspended database can wake. Results
    describe that identity, not a future Data API binding. At most 1000
    rows and 1 MiB of values are returned; truncated marks partial results.
    Cache-Control no-store.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresQueryResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresQueryResponse | Problem | None:
    """Inspect public-schema metadata for a Data API connection.

     Requires data:read (or admin) and MFA. Runs a fixed catalog-only
    statement as the provider-managed read-only query identity, in a
    bounded read-only transaction. No request body, customer table rows,
    SQL expressions or credentials are returned. The query identity may
    be created or refreshed and a suspended database can wake. Results
    describe that identity, not a future Data API binding. At most 1000
    rows and 1 MiB of values are returned; truncated marks partial results.
    Cache-Control no-store.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresQueryResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
