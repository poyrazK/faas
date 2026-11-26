from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.export_api_consumer_usage_statement_stripe_draft_body import (
    ExportAPIConsumerUsageStatementStripeDraftBody,
)
from ...models.problem import Problem
from ...models.statement_invoice_export_response import StatementInvoiceExportResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    consumer_id: UUID,
    statement_id: UUID,
    *,
    body: ExportAPIConsumerUsageStatementStripeDraftBody,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/consumers/{consumer_id}/usage-statements/{statement_id}/invoice/draft".format(
            slug=quote(str(slug), safe=""),
            consumer_id=quote(str(consumer_id), safe=""),
            statement_id=quote(str(statement_id), safe=""),
        ),
    }

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
    slug: str,
    consumer_id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ExportAPIConsumerUsageStatementStripeDraftBody,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | StatementInvoiceExportResponse]:
    """Export a finalized app consumer statement as a Stripe draft.

     Requires a Stripe connection set to draft collection. Refuses an
    unfinished export configured to send or charge, including when that
    export started concurrently. Requires the same Stripe test or live
    mode as the reviewed draft. Uses the verified customer link for that
    Stripe account and mode, falling back to a Stripe customer ID in the
    consumer's external reference. The export pins the customer for retries.
    Repeating the request returns the same draft. Requires a verified
    admin session with fresh MFA step-up.

    Args:
        slug (str):
        consumer_id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (ExportAPIConsumerUsageStatementStripeDraftBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StatementInvoiceExportResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        statement_id=statement_id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    consumer_id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ExportAPIConsumerUsageStatementStripeDraftBody,
    idempotency_key: str | Unset = UNSET,
) -> Problem | StatementInvoiceExportResponse | None:
    """Export a finalized app consumer statement as a Stripe draft.

     Requires a Stripe connection set to draft collection. Refuses an
    unfinished export configured to send or charge, including when that
    export started concurrently. Requires the same Stripe test or live
    mode as the reviewed draft. Uses the verified customer link for that
    Stripe account and mode, falling back to a Stripe customer ID in the
    consumer's external reference. The export pins the customer for retries.
    Repeating the request returns the same draft. Requires a verified
    admin session with fresh MFA step-up.

    Args:
        slug (str):
        consumer_id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (ExportAPIConsumerUsageStatementStripeDraftBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StatementInvoiceExportResponse
    """

    return sync_detailed(
        slug=slug,
        consumer_id=consumer_id,
        statement_id=statement_id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    consumer_id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ExportAPIConsumerUsageStatementStripeDraftBody,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | StatementInvoiceExportResponse]:
    """Export a finalized app consumer statement as a Stripe draft.

     Requires a Stripe connection set to draft collection. Refuses an
    unfinished export configured to send or charge, including when that
    export started concurrently. Requires the same Stripe test or live
    mode as the reviewed draft. Uses the verified customer link for that
    Stripe account and mode, falling back to a Stripe customer ID in the
    consumer's external reference. The export pins the customer for retries.
    Repeating the request returns the same draft. Requires a verified
    admin session with fresh MFA step-up.

    Args:
        slug (str):
        consumer_id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (ExportAPIConsumerUsageStatementStripeDraftBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StatementInvoiceExportResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        statement_id=statement_id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    consumer_id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ExportAPIConsumerUsageStatementStripeDraftBody,
    idempotency_key: str | Unset = UNSET,
) -> Problem | StatementInvoiceExportResponse | None:
    """Export a finalized app consumer statement as a Stripe draft.

     Requires a Stripe connection set to draft collection. Refuses an
    unfinished export configured to send or charge, including when that
    export started concurrently. Requires the same Stripe test or live
    mode as the reviewed draft. Uses the verified customer link for that
    Stripe account and mode, falling back to a Stripe customer ID in the
    consumer's external reference. The export pins the customer for retries.
    Repeating the request returns the same draft. Requires a verified
    admin session with fresh MFA step-up.

    Args:
        slug (str):
        consumer_id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (ExportAPIConsumerUsageStatementStripeDraftBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StatementInvoiceExportResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            consumer_id=consumer_id,
            statement_id=statement_id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
