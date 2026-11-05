from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_accounting_reconciliation_request import ManagedPostgresAccountingReconciliationRequest
from ...models.managed_postgres_accounting_reconciliation_result import ManagedPostgresAccountingReconciliationResult
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    account_id: UUID,
    *,
    body: ManagedPostgresAccountingReconciliationRequest,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/admin/managed-postgres/accounting/{account_id}/reconciliations".format(
            account_id=quote(str(account_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresAccountingReconciliationResult | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresAccountingReconciliationResult.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresAccountingReconciliationResult | Problem]:
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
    body: ManagedPostgresAccountingReconciliationRequest,
    idempotency_key: str,
) -> Response[ManagedPostgresAccountingReconciliationResult | Problem]:
    """Reconcile a legacy PostgreSQL identity and confirmed shutdown

     Allowlisted operator session with recent MFA step-up, same-origin checks and Idempotency-Key
    required. Bearer keys cannot apply. Requires a current expected_revision. Atomically attaches the
    evidence-backed identity and actual shutdown boundary, resets derived coverage and retains immutable
    before/after evidence. Existing usage quantities and accounting obligations remain intact. Recovery
    and final corrections remain required; this does not settle an invoice. reconciliation_id
    permanently deduplicates identical requests by the same actor.

    Args:
        account_id (UUID):
        idempotency_key (str):
        body (ManagedPostgresAccountingReconciliationRequest): Operator-attested immutable backend
            identity and actual provider shutdown for one unresolved legacy tombstone. The operator
            verifies artifact ownership and lineage; the server does not fetch or authenticate the
            source. Missing lookup results are not shutdown evidence.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresAccountingReconciliationResult | Problem]
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
    body: ManagedPostgresAccountingReconciliationRequest,
    idempotency_key: str,
) -> ManagedPostgresAccountingReconciliationResult | Problem | None:
    """Reconcile a legacy PostgreSQL identity and confirmed shutdown

     Allowlisted operator session with recent MFA step-up, same-origin checks and Idempotency-Key
    required. Bearer keys cannot apply. Requires a current expected_revision. Atomically attaches the
    evidence-backed identity and actual shutdown boundary, resets derived coverage and retains immutable
    before/after evidence. Existing usage quantities and accounting obligations remain intact. Recovery
    and final corrections remain required; this does not settle an invoice. reconciliation_id
    permanently deduplicates identical requests by the same actor.

    Args:
        account_id (UUID):
        idempotency_key (str):
        body (ManagedPostgresAccountingReconciliationRequest): Operator-attested immutable backend
            identity and actual provider shutdown for one unresolved legacy tombstone. The operator
            verifies artifact ownership and lineage; the server does not fetch or authenticate the
            source. Missing lookup results are not shutdown evidence.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresAccountingReconciliationResult | Problem
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
    body: ManagedPostgresAccountingReconciliationRequest,
    idempotency_key: str,
) -> Response[ManagedPostgresAccountingReconciliationResult | Problem]:
    """Reconcile a legacy PostgreSQL identity and confirmed shutdown

     Allowlisted operator session with recent MFA step-up, same-origin checks and Idempotency-Key
    required. Bearer keys cannot apply. Requires a current expected_revision. Atomically attaches the
    evidence-backed identity and actual shutdown boundary, resets derived coverage and retains immutable
    before/after evidence. Existing usage quantities and accounting obligations remain intact. Recovery
    and final corrections remain required; this does not settle an invoice. reconciliation_id
    permanently deduplicates identical requests by the same actor.

    Args:
        account_id (UUID):
        idempotency_key (str):
        body (ManagedPostgresAccountingReconciliationRequest): Operator-attested immutable backend
            identity and actual provider shutdown for one unresolved legacy tombstone. The operator
            verifies artifact ownership and lineage; the server does not fetch or authenticate the
            source. Missing lookup results are not shutdown evidence.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresAccountingReconciliationResult | Problem]
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
    body: ManagedPostgresAccountingReconciliationRequest,
    idempotency_key: str,
) -> ManagedPostgresAccountingReconciliationResult | Problem | None:
    """Reconcile a legacy PostgreSQL identity and confirmed shutdown

     Allowlisted operator session with recent MFA step-up, same-origin checks and Idempotency-Key
    required. Bearer keys cannot apply. Requires a current expected_revision. Atomically attaches the
    evidence-backed identity and actual shutdown boundary, resets derived coverage and retains immutable
    before/after evidence. Existing usage quantities and accounting obligations remain intact. Recovery
    and final corrections remain required; this does not settle an invoice. reconciliation_id
    permanently deduplicates identical requests by the same actor.

    Args:
        account_id (UUID):
        idempotency_key (str):
        body (ManagedPostgresAccountingReconciliationRequest): Operator-attested immutable backend
            identity and actual provider shutdown for one unresolved legacy tombstone. The operator
            verifies artifact ownership and lineage; the server does not fetch or authenticate the
            source. Missing lookup results are not shutdown evidence.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresAccountingReconciliationResult | Problem
    """

    return (
        await asyncio_detailed(
            account_id=account_id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
