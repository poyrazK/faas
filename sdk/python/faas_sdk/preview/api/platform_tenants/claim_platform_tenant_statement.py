from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.claim_api_consumer_usage_statement_request import ClaimAPIConsumerUsageStatementRequest
from ...models.platform_tenant_statement_handoff_response import PlatformTenantStatementHandoffResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    statement_id: UUID,
    *,
    body: ClaimAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenants/{id}/usage-statements/{statement_id}/handoff".format(
            id=quote(str(id), safe=""),
            statement_id=quote(str(statement_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantStatementHandoffResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantStatementHandoffResponse.from_dict(response.json())

        return response_200

    if response.status_code == 201:
        response_201 = PlatformTenantStatementHandoffResponse.from_dict(response.json())

        return response_201

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantStatementHandoffResponse | Problem]:
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
    body: ClaimAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[PlatformTenantStatementHandoffResponse | Problem]:
    """Record one provider-neutral external billing handoff.

     Rejects consumer windows already claimed by overlapping app-local statements. Adjustment revisions
    for this exact tenant and period may each be handed off once.

    Args:
        id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (ClaimAPIConsumerUsageStatementRequest): Customer billing system's external invoice
            reference for a finalized statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantStatementHandoffResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        statement_id=statement_id,
        body=body,
        idempotency_key=idempotency_key,
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
    body: ClaimAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> PlatformTenantStatementHandoffResponse | Problem | None:
    """Record one provider-neutral external billing handoff.

     Rejects consumer windows already claimed by overlapping app-local statements. Adjustment revisions
    for this exact tenant and period may each be handed off once.

    Args:
        id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (ClaimAPIConsumerUsageStatementRequest): Customer billing system's external invoice
            reference for a finalized statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantStatementHandoffResponse | Problem
    """

    return sync_detailed(
        id=id,
        statement_id=statement_id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ClaimAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[PlatformTenantStatementHandoffResponse | Problem]:
    """Record one provider-neutral external billing handoff.

     Rejects consumer windows already claimed by overlapping app-local statements. Adjustment revisions
    for this exact tenant and period may each be handed off once.

    Args:
        id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (ClaimAPIConsumerUsageStatementRequest): Customer billing system's external invoice
            reference for a finalized statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantStatementHandoffResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        statement_id=statement_id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ClaimAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> PlatformTenantStatementHandoffResponse | Problem | None:
    """Record one provider-neutral external billing handoff.

     Rejects consumer windows already claimed by overlapping app-local statements. Adjustment revisions
    for this exact tenant and period may each be handed off once.

    Args:
        id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (ClaimAPIConsumerUsageStatementRequest): Customer billing system's external invoice
            reference for a finalized statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantStatementHandoffResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            statement_id=statement_id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
