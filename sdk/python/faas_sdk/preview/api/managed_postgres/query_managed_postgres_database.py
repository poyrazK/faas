from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_query_request import ManagedPostgresQueryRequest
from ...models.managed_postgres_query_response import ManagedPostgresQueryResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: ManagedPostgresQueryRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/postgres/databases/{id}/query".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresQueryResponse | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresQueryResponse.from_dict(response.json())

        return response_200

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

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
    body: ManagedPostgresQueryRequest,
) -> Response[ManagedPostgresQueryResponse | Problem]:
    """Run one read-only SQL statement.

     Requires the data:read scope (or admin) and MFA.
    Runs one statement as the database's agent
    query login, a read-only login that reads the public schema, inside
    a READ ONLY transaction with a 5 s statement timeout and a 1 s lock
    timeout, then rolls back. Returns at most max_rows rows (default
    100, at most 1000) and 1 MiB of values, each in its PostgreSQL text
    form or null; truncated is true when a cap stopped reading. One
    query per database runs at a time. A suspended database wakes and
    its compute is billed as usage. The SQL and rows are never logged;
    the audit log records a SHA-256 of the statement. Cache-Control
    no-store.

    Args:
        id (str):
        body (ManagedPostgresQueryRequest): One read-only SQL statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresQueryResponse | Problem]
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
    body: ManagedPostgresQueryRequest,
) -> ManagedPostgresQueryResponse | Problem | None:
    """Run one read-only SQL statement.

     Requires the data:read scope (or admin) and MFA.
    Runs one statement as the database's agent
    query login, a read-only login that reads the public schema, inside
    a READ ONLY transaction with a 5 s statement timeout and a 1 s lock
    timeout, then rolls back. Returns at most max_rows rows (default
    100, at most 1000) and 1 MiB of values, each in its PostgreSQL text
    form or null; truncated is true when a cap stopped reading. One
    query per database runs at a time. A suspended database wakes and
    its compute is billed as usage. The SQL and rows are never logged;
    the audit log records a SHA-256 of the statement. Cache-Control
    no-store.

    Args:
        id (str):
        body (ManagedPostgresQueryRequest): One read-only SQL statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresQueryResponse | Problem
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
    body: ManagedPostgresQueryRequest,
) -> Response[ManagedPostgresQueryResponse | Problem]:
    """Run one read-only SQL statement.

     Requires the data:read scope (or admin) and MFA.
    Runs one statement as the database's agent
    query login, a read-only login that reads the public schema, inside
    a READ ONLY transaction with a 5 s statement timeout and a 1 s lock
    timeout, then rolls back. Returns at most max_rows rows (default
    100, at most 1000) and 1 MiB of values, each in its PostgreSQL text
    form or null; truncated is true when a cap stopped reading. One
    query per database runs at a time. A suspended database wakes and
    its compute is billed as usage. The SQL and rows are never logged;
    the audit log records a SHA-256 of the statement. Cache-Control
    no-store.

    Args:
        id (str):
        body (ManagedPostgresQueryRequest): One read-only SQL statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresQueryResponse | Problem]
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
    body: ManagedPostgresQueryRequest,
) -> ManagedPostgresQueryResponse | Problem | None:
    """Run one read-only SQL statement.

     Requires the data:read scope (or admin) and MFA.
    Runs one statement as the database's agent
    query login, a read-only login that reads the public schema, inside
    a READ ONLY transaction with a 5 s statement timeout and a 1 s lock
    timeout, then rolls back. Returns at most max_rows rows (default
    100, at most 1000) and 1 MiB of values, each in its PostgreSQL text
    form or null; truncated is true when a cap stopped reading. One
    query per database runs at a time. A suspended database wakes and
    its compute is billed as usage. The SQL and rows are never logged;
    the audit log records a SHA-256 of the statement. Cache-Control
    no-store.

    Args:
        id (str):
        body (ManagedPostgresQueryRequest): One read-only SQL statement.

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
            body=body,
        )
    ).parsed
