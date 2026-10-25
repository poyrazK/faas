from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.admin_refund_response import AdminRefundResponse
from ...models.problem import Problem
from ...models.refund_account_invoice_body import RefundAccountInvoiceBody
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: RefundAccountInvoiceBody,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/admin/accounts/{id}/refunds".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AdminRefundResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AdminRefundResponse.from_dict(response.json())

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
) -> Response[AdminRefundResponse | Problem]:
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
    body: RefundAccountInvoiceBody,
    idempotency_key: str,
) -> Response[AdminRefundResponse | Problem]:
    """Refund a paid Polar invoice (admin-only).

     `invoice_id` must identify a local Gregale invoice belonging to the
    target account. The current public-release implementation supports
    Polar order IDs and integer EUR cents. `Idempotency-Key` is required
    and is sent to the provider unchanged (up to 255 characters).

    Args:
        id (UUID):
        idempotency_key (str):
        body (RefundAccountInvoiceBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AdminRefundResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RefundAccountInvoiceBody,
    idempotency_key: str,
) -> AdminRefundResponse | Problem | None:
    """Refund a paid Polar invoice (admin-only).

     `invoice_id` must identify a local Gregale invoice belonging to the
    target account. The current public-release implementation supports
    Polar order IDs and integer EUR cents. `Idempotency-Key` is required
    and is sent to the provider unchanged (up to 255 characters).

    Args:
        id (UUID):
        idempotency_key (str):
        body (RefundAccountInvoiceBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AdminRefundResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RefundAccountInvoiceBody,
    idempotency_key: str,
) -> Response[AdminRefundResponse | Problem]:
    """Refund a paid Polar invoice (admin-only).

     `invoice_id` must identify a local Gregale invoice belonging to the
    target account. The current public-release implementation supports
    Polar order IDs and integer EUR cents. `Idempotency-Key` is required
    and is sent to the provider unchanged (up to 255 characters).

    Args:
        id (UUID):
        idempotency_key (str):
        body (RefundAccountInvoiceBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AdminRefundResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RefundAccountInvoiceBody,
    idempotency_key: str,
) -> AdminRefundResponse | Problem | None:
    """Refund a paid Polar invoice (admin-only).

     `invoice_id` must identify a local Gregale invoice belonging to the
    target account. The current public-release implementation supports
    Polar order IDs and integer EUR cents. `Idempotency-Key` is required
    and is sent to the provider unchanged (up to 255 characters).

    Args:
        id (UUID):
        idempotency_key (str):
        body (RefundAccountInvoiceBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AdminRefundResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
