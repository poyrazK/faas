from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.approve_billing_run_draft_export_request import ApproveBillingRunDraftExportRequest
from ...models.problem import Problem
from ...models.statement_invoice_export_response import StatementInvoiceExportResponse
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: ApproveBillingRunDraftExportRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/billing-runs/{id}/draft-exports".format(
            id=quote(str(id), safe=""),
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
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ApproveBillingRunDraftExportRequest,
) -> Response[Problem | StatementInvoiceExportResponse]:
    """Finalize a reviewed usage statement and export a Stripe draft.

     Rechecks the run item, revision, customer, Stripe connection and amount due after credits.
    The approval applies to one item of the selection. A retry resumes or returns the same export.

    Args:
        id (UUID):
        body (ApproveBillingRunDraftExportRequest): The exact Stripe draft and its amount due as
            shown in the review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StatementInvoiceExportResponse]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ApproveBillingRunDraftExportRequest,
) -> Problem | StatementInvoiceExportResponse | None:
    """Finalize a reviewed usage statement and export a Stripe draft.

     Rechecks the run item, revision, customer, Stripe connection and amount due after credits.
    The approval applies to one item of the selection. A retry resumes or returns the same export.

    Args:
        id (UUID):
        body (ApproveBillingRunDraftExportRequest): The exact Stripe draft and its amount due as
            shown in the review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StatementInvoiceExportResponse
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ApproveBillingRunDraftExportRequest,
) -> Response[Problem | StatementInvoiceExportResponse]:
    """Finalize a reviewed usage statement and export a Stripe draft.

     Rechecks the run item, revision, customer, Stripe connection and amount due after credits.
    The approval applies to one item of the selection. A retry resumes or returns the same export.

    Args:
        id (UUID):
        body (ApproveBillingRunDraftExportRequest): The exact Stripe draft and its amount due as
            shown in the review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StatementInvoiceExportResponse]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ApproveBillingRunDraftExportRequest,
) -> Problem | StatementInvoiceExportResponse | None:
    """Finalize a reviewed usage statement and export a Stripe draft.

     Rechecks the run item, revision, customer, Stripe connection and amount due after credits.
    The approval applies to one item of the selection. A retry resumes or returns the same export.

    Args:
        id (UUID):
        body (ApproveBillingRunDraftExportRequest): The exact Stripe draft and its amount due as
            shown in the review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StatementInvoiceExportResponse
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
