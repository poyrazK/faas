from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_reconciliation_receipt_response import PlatformTenantReconciliationReceiptResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    receipt_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/platform-tenants/{id}/reconciliations/{receipt_id}".format(
            id=quote(str(id), safe=""),
            receipt_id=quote(str(receipt_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantReconciliationReceiptResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantReconciliationReceiptResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantReconciliationReceiptResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    receipt_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[PlatformTenantReconciliationReceiptResponse | Problem]:
    """Read one immutable reconciliation receipt.

     Returns the exact applied plan hash, timestamp, and change list. Desired request bodies and hostname
    challenge tokens are never persisted in receipts.

    Args:
        id (UUID):
        receipt_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantReconciliationReceiptResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        receipt_id=receipt_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    receipt_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> PlatformTenantReconciliationReceiptResponse | Problem | None:
    """Read one immutable reconciliation receipt.

     Returns the exact applied plan hash, timestamp, and change list. Desired request bodies and hostname
    challenge tokens are never persisted in receipts.

    Args:
        id (UUID):
        receipt_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantReconciliationReceiptResponse | Problem
    """

    return sync_detailed(
        id=id,
        receipt_id=receipt_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    receipt_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[PlatformTenantReconciliationReceiptResponse | Problem]:
    """Read one immutable reconciliation receipt.

     Returns the exact applied plan hash, timestamp, and change list. Desired request bodies and hostname
    challenge tokens are never persisted in receipts.

    Args:
        id (UUID):
        receipt_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantReconciliationReceiptResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        receipt_id=receipt_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    receipt_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> PlatformTenantReconciliationReceiptResponse | Problem | None:
    """Read one immutable reconciliation receipt.

     Returns the exact applied plan hash, timestamp, and change list. Desired request bodies and hostname
    challenge tokens are never persisted in receipts.

    Args:
        id (UUID):
        receipt_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantReconciliationReceiptResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            receipt_id=receipt_id,
            client=client,
        )
    ).parsed
