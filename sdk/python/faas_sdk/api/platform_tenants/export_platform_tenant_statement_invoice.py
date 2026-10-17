from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_statement_invoice_request import CreateStatementInvoiceRequest
from ...models.problem import Problem
from ...models.statement_invoice_export_response import StatementInvoiceExportResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    statement_id: UUID,
    *,
    body: CreateStatementInvoiceRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenants/{id}/usage-statements/{statement_id}/invoice".format(
            id=quote(str(id), safe=""),
            statement_id=quote(str(statement_id), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
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
    id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateStatementInvoiceRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | StatementInvoiceExportResponse]:
    """Export a finalized platform tenant statement as an invoice in Stripe, Paddle or Lago.

     Exports one finalized platform tenant statement revision to the
    provider in the body (default stripe; connect it with
    PUT /v1/account/billing-connections/{provider}), ADR-1502 and ADR-1507.
    Stripe gets a draft invoice and Paddle a draft manually collected
    transaction; neither is finalized or sent. Lago issues a one-off
    invoice at once, without charging it. Items are one per rate card and
    unit price plus a rounding item, so the invoice equals the statement
    total rounded to the cent; Paddle and Lago receive one summary item
    when a line is negative. On success the statement's handoff records
    the provider invoice ID. Retrying after a failure resumes the same
    invoice; repeating a completed export returns it. A statement exports
    to one provider only. Two-decimal currencies only; Paddle allows USD,
    EUR and GBP.

    Args:
        id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (CreateStatementInvoiceRequest | Unset): Target of a statement invoice export.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StatementInvoiceExportResponse]
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
    body: CreateStatementInvoiceRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Problem | StatementInvoiceExportResponse | None:
    """Export a finalized platform tenant statement as an invoice in Stripe, Paddle or Lago.

     Exports one finalized platform tenant statement revision to the
    provider in the body (default stripe; connect it with
    PUT /v1/account/billing-connections/{provider}), ADR-1502 and ADR-1507.
    Stripe gets a draft invoice and Paddle a draft manually collected
    transaction; neither is finalized or sent. Lago issues a one-off
    invoice at once, without charging it. Items are one per rate card and
    unit price plus a rounding item, so the invoice equals the statement
    total rounded to the cent; Paddle and Lago receive one summary item
    when a line is negative. On success the statement's handoff records
    the provider invoice ID. Retrying after a failure resumes the same
    invoice; repeating a completed export returns it. A statement exports
    to one provider only. Two-decimal currencies only; Paddle allows USD,
    EUR and GBP.

    Args:
        id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (CreateStatementInvoiceRequest | Unset): Target of a statement invoice export.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StatementInvoiceExportResponse
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
    body: CreateStatementInvoiceRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | StatementInvoiceExportResponse]:
    """Export a finalized platform tenant statement as an invoice in Stripe, Paddle or Lago.

     Exports one finalized platform tenant statement revision to the
    provider in the body (default stripe; connect it with
    PUT /v1/account/billing-connections/{provider}), ADR-1502 and ADR-1507.
    Stripe gets a draft invoice and Paddle a draft manually collected
    transaction; neither is finalized or sent. Lago issues a one-off
    invoice at once, without charging it. Items are one per rate card and
    unit price plus a rounding item, so the invoice equals the statement
    total rounded to the cent; Paddle and Lago receive one summary item
    when a line is negative. On success the statement's handoff records
    the provider invoice ID. Retrying after a failure resumes the same
    invoice; repeating a completed export returns it. A statement exports
    to one provider only. Two-decimal currencies only; Paddle allows USD,
    EUR and GBP.

    Args:
        id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (CreateStatementInvoiceRequest | Unset): Target of a statement invoice export.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StatementInvoiceExportResponse]
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
    body: CreateStatementInvoiceRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Problem | StatementInvoiceExportResponse | None:
    """Export a finalized platform tenant statement as an invoice in Stripe, Paddle or Lago.

     Exports one finalized platform tenant statement revision to the
    provider in the body (default stripe; connect it with
    PUT /v1/account/billing-connections/{provider}), ADR-1502 and ADR-1507.
    Stripe gets a draft invoice and Paddle a draft manually collected
    transaction; neither is finalized or sent. Lago issues a one-off
    invoice at once, without charging it. Items are one per rate card and
    unit price plus a rounding item, so the invoice equals the statement
    total rounded to the cent; Paddle and Lago receive one summary item
    when a line is negative. On success the statement's handoff records
    the provider invoice ID. Retrying after a failure resumes the same
    invoice; repeating a completed export returns it. A statement exports
    to one provider only. Two-decimal currencies only; Paddle allows USD,
    EUR and GBP.

    Args:
        id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (CreateStatementInvoiceRequest | Unset): Target of a statement invoice export.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StatementInvoiceExportResponse
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
