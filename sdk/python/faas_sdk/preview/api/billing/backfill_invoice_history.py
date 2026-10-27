from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.invoice_history_backfill_response import InvoiceHistoryBackfillResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["cursor"] = cursor

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/invoices/backfill",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> InvoiceHistoryBackfillResponse | Problem | None:
    if response.status_code == 200:
        response_200 = InvoiceHistoryBackfillResponse.from_dict(response.json())

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
) -> Response[InvoiceHistoryBackfillResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> Response[InvoiceHistoryBackfillResponse | Problem]:
    """Import one bounded page of missing provider invoices.

     Requires usage:read and invoice-history session MFA. Scans at most 25
    invoices for the authenticated account's provider-qualified customer.
    The opaque next_cursor resumes the next page for the same provider
    customer. Existing natural-key matches are skipped without modifying
    webhook data. Imported documents preserve provider financial facts and
    carry an unknown historical Gregale plan, so credit proration fails
    closed until that plan is independently established. Unsupported
    currencies, statuses, incomplete totals, and absent billing periods are
    counted as skipped. has_more reports provider pagination at request time;
    it is not a stable snapshot guarantee. Each page commits atomically.

    Args:
        cursor (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InvoiceHistoryBackfillResponse | Problem]
    """

    kwargs = _get_kwargs(
        cursor=cursor,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> InvoiceHistoryBackfillResponse | Problem | None:
    """Import one bounded page of missing provider invoices.

     Requires usage:read and invoice-history session MFA. Scans at most 25
    invoices for the authenticated account's provider-qualified customer.
    The opaque next_cursor resumes the next page for the same provider
    customer. Existing natural-key matches are skipped without modifying
    webhook data. Imported documents preserve provider financial facts and
    carry an unknown historical Gregale plan, so credit proration fails
    closed until that plan is independently established. Unsupported
    currencies, statuses, incomplete totals, and absent billing periods are
    counted as skipped. has_more reports provider pagination at request time;
    it is not a stable snapshot guarantee. Each page commits atomically.

    Args:
        cursor (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InvoiceHistoryBackfillResponse | Problem
    """

    return sync_detailed(
        client=client,
        cursor=cursor,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> Response[InvoiceHistoryBackfillResponse | Problem]:
    """Import one bounded page of missing provider invoices.

     Requires usage:read and invoice-history session MFA. Scans at most 25
    invoices for the authenticated account's provider-qualified customer.
    The opaque next_cursor resumes the next page for the same provider
    customer. Existing natural-key matches are skipped without modifying
    webhook data. Imported documents preserve provider financial facts and
    carry an unknown historical Gregale plan, so credit proration fails
    closed until that plan is independently established. Unsupported
    currencies, statuses, incomplete totals, and absent billing periods are
    counted as skipped. has_more reports provider pagination at request time;
    it is not a stable snapshot guarantee. Each page commits atomically.

    Args:
        cursor (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InvoiceHistoryBackfillResponse | Problem]
    """

    kwargs = _get_kwargs(
        cursor=cursor,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> InvoiceHistoryBackfillResponse | Problem | None:
    """Import one bounded page of missing provider invoices.

     Requires usage:read and invoice-history session MFA. Scans at most 25
    invoices for the authenticated account's provider-qualified customer.
    The opaque next_cursor resumes the next page for the same provider
    customer. Existing natural-key matches are skipped without modifying
    webhook data. Imported documents preserve provider financial facts and
    carry an unknown historical Gregale plan, so credit proration fails
    closed until that plan is independently established. Unsupported
    currencies, statuses, incomplete totals, and absent billing periods are
    counted as skipped. has_more reports provider pagination at request time;
    it is not a stable snapshot guarantee. Each page commits atomically.

    Args:
        cursor (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InvoiceHistoryBackfillResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            cursor=cursor,
            limit=limit,
        )
    ).parsed
