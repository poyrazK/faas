from http import HTTPStatus
from io import BytesIO
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.export_focus_invoices_format import ExportFOCUSInvoicesFormat
from ...models.problem import Problem
from ...types import UNSET, File, Response, Unset


def _get_kwargs(
    *,
    month: str,
    format_: ExportFOCUSInvoicesFormat | Unset = "zip",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["month"] = month

    json_format_: str | Unset = UNSET
    if not isinstance(format_, Unset):
        json_format_ = format_

    params["format"] = json_format_

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/billing/focus",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> File | Problem | None:
    if response.status_code == 200:
        response_200 = File(payload=BytesIO(response.content))

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[File | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    month: str,
    format_: ExportFOCUSInvoicesFormat | Unset = "zip",
) -> Response[File | Problem]:
    """Download a partial FOCUS 1.4 Invoice Detail projection.

     Requires usage:read and the same session MFA gate as invoice history.
    Includes only the authenticated account's locally persisted invoices
    whose period_end falls in the requested UTC month. Draft and void
    invoices are excluded and counted in metadata. The default ZIP binds
    CSV and metadata to one store snapshot; separate downloads may see
    newer webhooks. Amounts use exact two-decimal ISO currencies and split
    non-tax charges from tax. No current plan prices are substituted.

    This projection is partial: required PaymentTerms is empty because
    provider payment terms are not persisted. It does not claim complete
    FOCUS conformance or expose the Cost and Usage dataset. Issue and due
    dates, payment-currency conversions, purchase orders, provider line
    items, and separate credit/refund documents are unavailable. See
    /docs/billing#focus-invoice-export and the metadata limitations.

    At most 1000 stored invoices are read per month (including excluded
    invoices); a larger set returns 422 without a truncated artifact.
    Invalid stored amounts, currencies, identifiers, or dates return 409
    before any artifact bytes are sent. Each artifact is at most 3 MiB.

    Args:
        month (str):
        format_ (ExportFOCUSInvoicesFormat | Unset):  Default: 'zip'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[File | Problem]
    """

    kwargs = _get_kwargs(
        month=month,
        format_=format_,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    month: str,
    format_: ExportFOCUSInvoicesFormat | Unset = "zip",
) -> File | Problem | None:
    """Download a partial FOCUS 1.4 Invoice Detail projection.

     Requires usage:read and the same session MFA gate as invoice history.
    Includes only the authenticated account's locally persisted invoices
    whose period_end falls in the requested UTC month. Draft and void
    invoices are excluded and counted in metadata. The default ZIP binds
    CSV and metadata to one store snapshot; separate downloads may see
    newer webhooks. Amounts use exact two-decimal ISO currencies and split
    non-tax charges from tax. No current plan prices are substituted.

    This projection is partial: required PaymentTerms is empty because
    provider payment terms are not persisted. It does not claim complete
    FOCUS conformance or expose the Cost and Usage dataset. Issue and due
    dates, payment-currency conversions, purchase orders, provider line
    items, and separate credit/refund documents are unavailable. See
    /docs/billing#focus-invoice-export and the metadata limitations.

    At most 1000 stored invoices are read per month (including excluded
    invoices); a larger set returns 422 without a truncated artifact.
    Invalid stored amounts, currencies, identifiers, or dates return 409
    before any artifact bytes are sent. Each artifact is at most 3 MiB.

    Args:
        month (str):
        format_ (ExportFOCUSInvoicesFormat | Unset):  Default: 'zip'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        File | Problem
    """

    return sync_detailed(
        client=client,
        month=month,
        format_=format_,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    month: str,
    format_: ExportFOCUSInvoicesFormat | Unset = "zip",
) -> Response[File | Problem]:
    """Download a partial FOCUS 1.4 Invoice Detail projection.

     Requires usage:read and the same session MFA gate as invoice history.
    Includes only the authenticated account's locally persisted invoices
    whose period_end falls in the requested UTC month. Draft and void
    invoices are excluded and counted in metadata. The default ZIP binds
    CSV and metadata to one store snapshot; separate downloads may see
    newer webhooks. Amounts use exact two-decimal ISO currencies and split
    non-tax charges from tax. No current plan prices are substituted.

    This projection is partial: required PaymentTerms is empty because
    provider payment terms are not persisted. It does not claim complete
    FOCUS conformance or expose the Cost and Usage dataset. Issue and due
    dates, payment-currency conversions, purchase orders, provider line
    items, and separate credit/refund documents are unavailable. See
    /docs/billing#focus-invoice-export and the metadata limitations.

    At most 1000 stored invoices are read per month (including excluded
    invoices); a larger set returns 422 without a truncated artifact.
    Invalid stored amounts, currencies, identifiers, or dates return 409
    before any artifact bytes are sent. Each artifact is at most 3 MiB.

    Args:
        month (str):
        format_ (ExportFOCUSInvoicesFormat | Unset):  Default: 'zip'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[File | Problem]
    """

    kwargs = _get_kwargs(
        month=month,
        format_=format_,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    month: str,
    format_: ExportFOCUSInvoicesFormat | Unset = "zip",
) -> File | Problem | None:
    """Download a partial FOCUS 1.4 Invoice Detail projection.

     Requires usage:read and the same session MFA gate as invoice history.
    Includes only the authenticated account's locally persisted invoices
    whose period_end falls in the requested UTC month. Draft and void
    invoices are excluded and counted in metadata. The default ZIP binds
    CSV and metadata to one store snapshot; separate downloads may see
    newer webhooks. Amounts use exact two-decimal ISO currencies and split
    non-tax charges from tax. No current plan prices are substituted.

    This projection is partial: required PaymentTerms is empty because
    provider payment terms are not persisted. It does not claim complete
    FOCUS conformance or expose the Cost and Usage dataset. Issue and due
    dates, payment-currency conversions, purchase orders, provider line
    items, and separate credit/refund documents are unavailable. See
    /docs/billing#focus-invoice-export and the metadata limitations.

    At most 1000 stored invoices are read per month (including excluded
    invoices); a larger set returns 422 without a truncated artifact.
    Invalid stored amounts, currencies, identifiers, or dates return 409
    before any artifact bytes are sent. Each artifact is at most 3 MiB.

    Args:
        month (str):
        format_ (ExportFOCUSInvoicesFormat | Unset):  Default: 'zip'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        File | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            month=month,
            format_=format_,
        )
    ).parsed
