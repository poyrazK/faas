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
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/admin/managed-postgres/accounting/{account_id}/usage-imports".format(
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
    client: AuthenticatedClient,
    body: ManagedPostgresUsageImportRequest,
    idempotency_key: str,
) -> Response[ManagedPostgresUsageImportResult | Problem]:
    """Apply retained managed PostgreSQL usage evidence

     Allowlisted operator session with recent MFA step-up, same-origin checks, and Idempotency-Key
    required. Bearer API keys cannot apply imports. Requires expected_revision from preview. Commits
    normalized ledger windows, contiguous coverage, and immutable before/after evidence atomically.
    import_id durably deduplicates an identical request by the same actor; conflicting reuse is
    rejected. Evidence observation times are preserved. This neither establishes missing identities or
    shutdown nor reconciles a final provider invoice.

    Args:
        account_id (UUID):
        idempotency_key (str):
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
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    account_id: UUID,
    *,
    client: AuthenticatedClient,
    body: ManagedPostgresUsageImportRequest,
    idempotency_key: str,
) -> ManagedPostgresUsageImportResult | Problem | None:
    """Apply retained managed PostgreSQL usage evidence

     Allowlisted operator session with recent MFA step-up, same-origin checks, and Idempotency-Key
    required. Bearer API keys cannot apply imports. Requires expected_revision from preview. Commits
    normalized ledger windows, contiguous coverage, and immutable before/after evidence atomically.
    import_id durably deduplicates an identical request by the same actor; conflicting reuse is
    rejected. Evidence observation times are preserved. This neither establishes missing identities or
    shutdown nor reconciles a final provider invoice.

    Args:
        account_id (UUID):
        idempotency_key (str):
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
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    account_id: UUID,
    *,
    client: AuthenticatedClient,
    body: ManagedPostgresUsageImportRequest,
    idempotency_key: str,
) -> Response[ManagedPostgresUsageImportResult | Problem]:
    """Apply retained managed PostgreSQL usage evidence

     Allowlisted operator session with recent MFA step-up, same-origin checks, and Idempotency-Key
    required. Bearer API keys cannot apply imports. Requires expected_revision from preview. Commits
    normalized ledger windows, contiguous coverage, and immutable before/after evidence atomically.
    import_id durably deduplicates an identical request by the same actor; conflicting reuse is
    rejected. Evidence observation times are preserved. This neither establishes missing identities or
    shutdown nor reconciles a final provider invoice.

    Args:
        account_id (UUID):
        idempotency_key (str):
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
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    account_id: UUID,
    *,
    client: AuthenticatedClient,
    body: ManagedPostgresUsageImportRequest,
    idempotency_key: str,
) -> ManagedPostgresUsageImportResult | Problem | None:
    """Apply retained managed PostgreSQL usage evidence

     Allowlisted operator session with recent MFA step-up, same-origin checks, and Idempotency-Key
    required. Bearer API keys cannot apply imports. Requires expected_revision from preview. Commits
    normalized ledger windows, contiguous coverage, and immutable before/after evidence atomically.
    import_id durably deduplicates an identical request by the same actor; conflicting reuse is
    rejected. Evidence observation times are preserved. This neither establishes missing identities or
    shutdown nor reconciles a final provider invoice.

    Args:
        account_id (UUID):
        idempotency_key (str):
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
            idempotency_key=idempotency_key,
        )
    ).parsed
