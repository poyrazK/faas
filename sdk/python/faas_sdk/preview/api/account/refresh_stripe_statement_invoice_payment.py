from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.statement_invoice_export_response import StatementInvoiceExportResponse
from ...types import Response


def _get_kwargs(
    statement_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/statement-invoices/{statement_id}/refresh".format(
            statement_id=quote(str(statement_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | StatementInvoiceExportResponse | None:
    if response.status_code == 200:
        response_200 = StatementInvoiceExportResponse.from_dict(response.json())

        return response_200

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

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | StatementInvoiceExportResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | StatementInvoiceExportResponse]:
    """Check the current Stripe payment status of one exported app consumer invoice.

     Reads the invoice from Stripe and updates Gregale's recorded balance after verifying the invoice
    identity and mode.

    Args:
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StatementInvoiceExportResponse]
    """

    kwargs = _get_kwargs(
        statement_id=statement_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | StatementInvoiceExportResponse | None:
    """Check the current Stripe payment status of one exported app consumer invoice.

     Reads the invoice from Stripe and updates Gregale's recorded balance after verifying the invoice
    identity and mode.

    Args:
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StatementInvoiceExportResponse
    """

    return sync_detailed(
        statement_id=statement_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | StatementInvoiceExportResponse]:
    """Check the current Stripe payment status of one exported app consumer invoice.

     Reads the invoice from Stripe and updates Gregale's recorded balance after verifying the invoice
    identity and mode.

    Args:
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StatementInvoiceExportResponse]
    """

    kwargs = _get_kwargs(
        statement_id=statement_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | StatementInvoiceExportResponse | None:
    """Check the current Stripe payment status of one exported app consumer invoice.

     Reads the invoice from Stripe and updates Gregale's recorded balance after verifying the invoice
    identity and mode.

    Args:
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StatementInvoiceExportResponse
    """

    return (
        await asyncio_detailed(
            statement_id=statement_id,
            client=client,
        )
    ).parsed
