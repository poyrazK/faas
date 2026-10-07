from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_usage_import_request import ManagedPostgresUsageImportRequest
from ...models.managed_postgres_usage_import_result import ManagedPostgresUsageImportResult
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    account_id: UUID,
    *,
    body: ManagedPostgresUsageImportRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/admin/managed-postgres/accounting/{account_id}/usage-imports/preview".format(
            account_id=quote(str(account_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresUsageImportResult | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresUsageImportResult.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresUsageImportResult | Problem]:
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
    body: ManagedPostgresUsageImportRequest,
) -> Response[ManagedPostgresUsageImportResult | Problem]:
    """Preview retained managed PostgreSQL usage evidence

     Operator allowlist and admin scope required, with the existing session MFA gate. Validates
    normalized complete windows against local catalog and ledger evidence. Makes no provider calls or
    writes. Returns the revision required for apply; concurrent ledger or lifecycle changes invalidate
    it.

    Args:
        account_id (UUID):
        body (ManagedPostgresUsageImportRequest): Operator-attested retained usage for a known
            independently accounted database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresUsageImportResult | Problem]
    """

    kwargs = _get_kwargs(
        account_id=account_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    account_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedPostgresUsageImportRequest,
) -> ManagedPostgresUsageImportResult | Problem | None:
    """Preview retained managed PostgreSQL usage evidence

     Operator allowlist and admin scope required, with the existing session MFA gate. Validates
    normalized complete windows against local catalog and ledger evidence. Makes no provider calls or
    writes. Returns the revision required for apply; concurrent ledger or lifecycle changes invalidate
    it.

    Args:
        account_id (UUID):
        body (ManagedPostgresUsageImportRequest): Operator-attested retained usage for a known
            independently accounted database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresUsageImportResult | Problem
    """

    return sync_detailed(
        account_id=account_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    account_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedPostgresUsageImportRequest,
) -> Response[ManagedPostgresUsageImportResult | Problem]:
    """Preview retained managed PostgreSQL usage evidence

     Operator allowlist and admin scope required, with the existing session MFA gate. Validates
    normalized complete windows against local catalog and ledger evidence. Makes no provider calls or
    writes. Returns the revision required for apply; concurrent ledger or lifecycle changes invalidate
    it.

    Args:
        account_id (UUID):
        body (ManagedPostgresUsageImportRequest): Operator-attested retained usage for a known
            independently accounted database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresUsageImportResult | Problem]
    """

    kwargs = _get_kwargs(
        account_id=account_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    account_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedPostgresUsageImportRequest,
) -> ManagedPostgresUsageImportResult | Problem | None:
    """Preview retained managed PostgreSQL usage evidence

     Operator allowlist and admin scope required, with the existing session MFA gate. Validates
    normalized complete windows against local catalog and ledger evidence. Makes no provider calls or
    writes. Returns the revision required for apply; concurrent ledger or lifecycle changes invalidate
    it.

    Args:
        account_id (UUID):
        body (ManagedPostgresUsageImportRequest): Operator-attested retained usage for a known
            independently accounted database.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresUsageImportResult | Problem
    """

    return (
        await asyncio_detailed(
            account_id=account_id,
            client=client,
            body=body,
        )
    ).parsed
