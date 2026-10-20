from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.billing_reconcile_response import BillingReconcileResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/admin/billing-reconcile/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BillingReconcileResponse | Problem | None:
    if response.status_code == 200:
        response_200 = BillingReconcileResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[BillingReconcileResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingReconcileResponse | Problem]:
    """Run a single-account reconcile against the active billing Provider (admin-only).

     Loads the account, calls billing.Provider.ReconcileUsage for
    a rolling 30-day window [start, end). Stripe implements
    this (ADR-049 §B.1); Paddle returns billing.ErrNotImplemented
    and the handler maps to 501. The response surfaces the
    SDK-returned mb_seconds total so an operator can diff
    against the local usage_minutes sum.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingReconcileResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> BillingReconcileResponse | Problem | None:
    """Run a single-account reconcile against the active billing Provider (admin-only).

     Loads the account, calls billing.Provider.ReconcileUsage for
    a rolling 30-day window [start, end). Stripe implements
    this (ADR-049 §B.1); Paddle returns billing.ErrNotImplemented
    and the handler maps to 501. The response surfaces the
    SDK-returned mb_seconds total so an operator can diff
    against the local usage_minutes sum.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingReconcileResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingReconcileResponse | Problem]:
    """Run a single-account reconcile against the active billing Provider (admin-only).

     Loads the account, calls billing.Provider.ReconcileUsage for
    a rolling 30-day window [start, end). Stripe implements
    this (ADR-049 §B.1); Paddle returns billing.ErrNotImplemented
    and the handler maps to 501. The response surfaces the
    SDK-returned mb_seconds total so an operator can diff
    against the local usage_minutes sum.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingReconcileResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> BillingReconcileResponse | Problem | None:
    """Run a single-account reconcile against the active billing Provider (admin-only).

     Loads the account, calls billing.Provider.ReconcileUsage for
    a rolling 30-day window [start, end). Stripe implements
    this (ADR-049 §B.1); Paddle returns billing.ErrNotImplemented
    and the handler maps to 501. The response surfaces the
    SDK-returned mb_seconds total so an operator can diff
    against the local usage_minutes sum.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingReconcileResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
