from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.billing_retry_response import BillingRetryResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/billing/retry",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BillingRetryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = BillingRetryResponse.from_dict(response.json())

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
) -> Response[BillingRetryResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingRetryResponse | Problem]:
    """Retry the latest unpaid invoice / transaction for this account.

     The configured provider is asked to retry the most recent unpaid
    charge. The Idempotency-Key header (auto UUIDv4 if absent) is
    forwarded where the provider supports it. Providers without a
    direct retry API return 501 and the response includes a portal URL
    for payment-method recovery.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingRetryResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> BillingRetryResponse | Problem | None:
    """Retry the latest unpaid invoice / transaction for this account.

     The configured provider is asked to retry the most recent unpaid
    charge. The Idempotency-Key header (auto UUIDv4 if absent) is
    forwarded where the provider supports it. Providers without a
    direct retry API return 501 and the response includes a portal URL
    for payment-method recovery.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingRetryResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingRetryResponse | Problem]:
    """Retry the latest unpaid invoice / transaction for this account.

     The configured provider is asked to retry the most recent unpaid
    charge. The Idempotency-Key header (auto UUIDv4 if absent) is
    forwarded where the provider supports it. Providers without a
    direct retry API return 501 and the response includes a portal URL
    for payment-method recovery.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingRetryResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> BillingRetryResponse | Problem | None:
    """Retry the latest unpaid invoice / transaction for this account.

     The configured provider is asked to retry the most recent unpaid
    charge. The Idempotency-Key header (auto UUIDv4 if absent) is
    forwarded where the provider supports it. Providers without a
    direct retry API return 501 and the response includes a portal URL
    for payment-method recovery.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingRetryResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
