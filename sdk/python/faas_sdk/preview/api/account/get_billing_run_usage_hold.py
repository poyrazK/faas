from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.billing_run_usage_hold import BillingRunUsageHold
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    consumer_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/billing-runs/{id}/usage-holds/{consumer_id}".format(
            id=quote(str(id), safe=""),
            consumer_id=quote(str(consumer_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BillingRunUsageHold | Problem | None:
    if response.status_code == 200:
        response_200 = BillingRunUsageHold.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[BillingRunUsageHold | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingRunUsageHold | Problem]:
    """Read the captured evidence for a billing run usage hold.

     Compares the recorded minimum with the current billing ledger for each affected UTC hour.

    Args:
        id (UUID):
        consumer_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingRunUsageHold | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        consumer_id=consumer_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> BillingRunUsageHold | Problem | None:
    """Read the captured evidence for a billing run usage hold.

     Compares the recorded minimum with the current billing ledger for each affected UTC hour.

    Args:
        id (UUID):
        consumer_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingRunUsageHold | Problem
    """

    return sync_detailed(
        id=id,
        consumer_id=consumer_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[BillingRunUsageHold | Problem]:
    """Read the captured evidence for a billing run usage hold.

     Compares the recorded minimum with the current billing ledger for each affected UTC hour.

    Args:
        id (UUID):
        consumer_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingRunUsageHold | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        consumer_id=consumer_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> BillingRunUsageHold | Problem | None:
    """Read the captured evidence for a billing run usage hold.

     Compares the recorded minimum with the current billing ledger for each affected UTC hour.

    Args:
        id (UUID):
        consumer_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingRunUsageHold | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            consumer_id=consumer_id,
            client=client,
        )
    ).parsed
