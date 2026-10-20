from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_accounting_diagnostics_response import ManagedPostgresAccountingDiagnosticsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    account_id: UUID,
    *,
    after: UUID | Unset = UNSET,
    limit: int | Unset = 50,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_after: str | Unset = UNSET
    if not isinstance(after, Unset):
        json_after = str(after)
    params["after"] = json_after

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/managed-postgres/accounting/{account_id}".format(
            account_id=quote(str(account_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresAccountingDiagnosticsResponse | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresAccountingDiagnosticsResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresAccountingDiagnosticsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    account_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after: UUID | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[ManagedPostgresAccountingDiagnosticsResponse | Problem]:
    """Explain managed PostgreSQL accounting blockers

     Operator allowlist and admin scope required, with the existing session MFA gate (bearer API keys
    follow IAM policy). Reads local catalog and ledger evidence only. Each page is one database
    snapshot; pages are live views, not a frozen account snapshot. No provider requests, opaque provider
    IDs, or credentials.

    Args:
        account_id (UUID):
        after (UUID | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresAccountingDiagnosticsResponse | Problem]
    """

    kwargs = _get_kwargs(
        account_id=account_id,
        after=after,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    account_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after: UUID | Unset = UNSET,
    limit: int | Unset = 50,
) -> ManagedPostgresAccountingDiagnosticsResponse | Problem | None:
    """Explain managed PostgreSQL accounting blockers

     Operator allowlist and admin scope required, with the existing session MFA gate (bearer API keys
    follow IAM policy). Reads local catalog and ledger evidence only. Each page is one database
    snapshot; pages are live views, not a frozen account snapshot. No provider requests, opaque provider
    IDs, or credentials.

    Args:
        account_id (UUID):
        after (UUID | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresAccountingDiagnosticsResponse | Problem
    """

    return sync_detailed(
        account_id=account_id,
        client=client,
        after=after,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    account_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after: UUID | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[ManagedPostgresAccountingDiagnosticsResponse | Problem]:
    """Explain managed PostgreSQL accounting blockers

     Operator allowlist and admin scope required, with the existing session MFA gate (bearer API keys
    follow IAM policy). Reads local catalog and ledger evidence only. Each page is one database
    snapshot; pages are live views, not a frozen account snapshot. No provider requests, opaque provider
    IDs, or credentials.

    Args:
        account_id (UUID):
        after (UUID | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresAccountingDiagnosticsResponse | Problem]
    """

    kwargs = _get_kwargs(
        account_id=account_id,
        after=after,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    account_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after: UUID | Unset = UNSET,
    limit: int | Unset = 50,
) -> ManagedPostgresAccountingDiagnosticsResponse | Problem | None:
    """Explain managed PostgreSQL accounting blockers

     Operator allowlist and admin scope required, with the existing session MFA gate (bearer API keys
    follow IAM policy). Reads local catalog and ledger evidence only. Each page is one database
    snapshot; pages are live views, not a frozen account snapshot. No provider requests, opaque provider
    IDs, or credentials.

    Args:
        account_id (UUID):
        after (UUID | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresAccountingDiagnosticsResponse | Problem
    """

    return (
        await asyncio_detailed(
            account_id=account_id,
            client=client,
            after=after,
            limit=limit,
        )
    ).parsed
