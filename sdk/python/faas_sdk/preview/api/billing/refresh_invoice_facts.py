from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.invoice_refresh_response import InvoiceRefreshResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/invoices/{id}/refresh".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> InvoiceRefreshResponse | Problem | None:
    if response.status_code == 200:
        response_200 = InvoiceRefreshResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[InvoiceRefreshResponse | Problem]:
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
) -> Response[InvoiceRefreshResponse | Problem]:
    """Refresh provider facts for an existing account invoice.

     Requires usage:read and the invoice-history session MFA gate. Fetches
    the configured provider's invoice, transaction, or order using the
    account's provider-qualified customer identity. Accepts no body or query
    parameters. Only invoice details and their lifecycle history change;
    monetary values, payment state, refunds, credits, and plan are preserved.
    Provider identity, currency, total, and tax must match the captured local
    invoice. A concurrent invoice update returns 409, allowing a fresh retry.
    Stripe paginates all lines and resolves opaque price/plan IDs. Unknown
    facts and classifications remain source gaps. The operation is bounded
    to 32 provider reads, 1,000 items, 4 MiB per response, and two minutes.
    It enriches known invoices; it does not discover missing provider history.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InvoiceRefreshResponse | Problem]
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
) -> InvoiceRefreshResponse | Problem | None:
    """Refresh provider facts for an existing account invoice.

     Requires usage:read and the invoice-history session MFA gate. Fetches
    the configured provider's invoice, transaction, or order using the
    account's provider-qualified customer identity. Accepts no body or query
    parameters. Only invoice details and their lifecycle history change;
    monetary values, payment state, refunds, credits, and plan are preserved.
    Provider identity, currency, total, and tax must match the captured local
    invoice. A concurrent invoice update returns 409, allowing a fresh retry.
    Stripe paginates all lines and resolves opaque price/plan IDs. Unknown
    facts and classifications remain source gaps. The operation is bounded
    to 32 provider reads, 1,000 items, 4 MiB per response, and two minutes.
    It enriches known invoices; it does not discover missing provider history.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InvoiceRefreshResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[InvoiceRefreshResponse | Problem]:
    """Refresh provider facts for an existing account invoice.

     Requires usage:read and the invoice-history session MFA gate. Fetches
    the configured provider's invoice, transaction, or order using the
    account's provider-qualified customer identity. Accepts no body or query
    parameters. Only invoice details and their lifecycle history change;
    monetary values, payment state, refunds, credits, and plan are preserved.
    Provider identity, currency, total, and tax must match the captured local
    invoice. A concurrent invoice update returns 409, allowing a fresh retry.
    Stripe paginates all lines and resolves opaque price/plan IDs. Unknown
    facts and classifications remain source gaps. The operation is bounded
    to 32 provider reads, 1,000 items, 4 MiB per response, and two minutes.
    It enriches known invoices; it does not discover missing provider history.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InvoiceRefreshResponse | Problem]
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
) -> InvoiceRefreshResponse | Problem | None:
    """Refresh provider facts for an existing account invoice.

     Requires usage:read and the invoice-history session MFA gate. Fetches
    the configured provider's invoice, transaction, or order using the
    account's provider-qualified customer identity. Accepts no body or query
    parameters. Only invoice details and their lifecycle history change;
    monetary values, payment state, refunds, credits, and plan are preserved.
    Provider identity, currency, total, and tax must match the captured local
    invoice. A concurrent invoice update returns 409, allowing a fresh retry.
    Stripe paginates all lines and resolves opaque price/plan IDs. Unknown
    facts and classifications remain source gaps. The operation is bounded
    to 32 provider reads, 1,000 items, 4 MiB per response, and two minutes.
    It enriches known invoices; it does not discover missing provider history.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InvoiceRefreshResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
