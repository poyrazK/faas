from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.billing_paddle_overage_preflight_response import BillingPaddleOveragePreflightResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/billing-paddle-overage/preflight",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BillingPaddleOveragePreflightResponse | Problem | None:
    if response.status_code == 200:
        response_200 = BillingPaddleOveragePreflightResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[BillingPaddleOveragePreflightResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingPaddleOveragePreflightResponse | Problem]:
    """Read the paddle_overage_dedupe schema probe (admin-only).

     Operator-side guard for the Paddle overage pusher's
    per-window claim state machine (migration 00041). Returns
    which of the four new columns are present + the per-state
    row counts so an operator can verify the meterd loop will
    not crash on a 42703 (column missing) error before any
    customer-facing push is attempted.

    Returned by the B4 pre-flight CLI subcommand. A response
    with table_exists=true and any of has_window_start /
    has_state / has_claimed_at / has_claimed_by=false means
    migration 00041 was not (fully) applied. A response with
    table_exists=false means migrations 00034 + 00041 are
    both unapplied (the table has never been created).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingPaddleOveragePreflightResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> BillingPaddleOveragePreflightResponse | Problem | None:
    """Read the paddle_overage_dedupe schema probe (admin-only).

     Operator-side guard for the Paddle overage pusher's
    per-window claim state machine (migration 00041). Returns
    which of the four new columns are present + the per-state
    row counts so an operator can verify the meterd loop will
    not crash on a 42703 (column missing) error before any
    customer-facing push is attempted.

    Returned by the B4 pre-flight CLI subcommand. A response
    with table_exists=true and any of has_window_start /
    has_state / has_claimed_at / has_claimed_by=false means
    migration 00041 was not (fully) applied. A response with
    table_exists=false means migrations 00034 + 00041 are
    both unapplied (the table has never been created).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingPaddleOveragePreflightResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingPaddleOveragePreflightResponse | Problem]:
    """Read the paddle_overage_dedupe schema probe (admin-only).

     Operator-side guard for the Paddle overage pusher's
    per-window claim state machine (migration 00041). Returns
    which of the four new columns are present + the per-state
    row counts so an operator can verify the meterd loop will
    not crash on a 42703 (column missing) error before any
    customer-facing push is attempted.

    Returned by the B4 pre-flight CLI subcommand. A response
    with table_exists=true and any of has_window_start /
    has_state / has_claimed_at / has_claimed_by=false means
    migration 00041 was not (fully) applied. A response with
    table_exists=false means migrations 00034 + 00041 are
    both unapplied (the table has never been created).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingPaddleOveragePreflightResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> BillingPaddleOveragePreflightResponse | Problem | None:
    """Read the paddle_overage_dedupe schema probe (admin-only).

     Operator-side guard for the Paddle overage pusher's
    per-window claim state machine (migration 00041). Returns
    which of the four new columns are present + the per-state
    row counts so an operator can verify the meterd loop will
    not crash on a 42703 (column missing) error before any
    customer-facing push is attempted.

    Returned by the B4 pre-flight CLI subcommand. A response
    with table_exists=true and any of has_window_start /
    has_state / has_claimed_at / has_claimed_by=false means
    migration 00041 was not (fully) applied. A response with
    table_exists=false means migrations 00034 + 00041 are
    both unapplied (the table has never been created).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingPaddleOveragePreflightResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
