from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.billing_run_response import BillingRunResponse
from ...models.create_billing_run_request import CreateBillingRunRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: CreateBillingRunRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/billing-runs",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BillingRunResponse | Problem | None:
    if response.status_code == 202:
        response_202 = BillingRunResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[BillingRunResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateBillingRunRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[BillingRunResponse | Problem]:
    """Bill one ended month for every customer.

     Starts a background run for one UTC calendar month that ended at least
    an hour ago. For every app consumer and platform tenant with billable
    usage, it drafts the month's statement, holds consumers whose ledger
    misses requests telemetry saw (`hold_on_gaps`), finalizes it, and with
    `export` set to stripe, paddle or lago exports each statement to that provider. Usage attributed to
    a
    platform tenant is billed on the tenant's statement. One run per
    account can be queued or running at a time.

    Args:
        idempotency_key (str | Unset):
        body (CreateBillingRunRequest): A month-end billing run. Omitted options bill both
            sources, finalize, do not export, and hold consumers with completeness gaps.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingRunResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: CreateBillingRunRequest,
    idempotency_key: str | Unset = UNSET,
) -> BillingRunResponse | Problem | None:
    """Bill one ended month for every customer.

     Starts a background run for one UTC calendar month that ended at least
    an hour ago. For every app consumer and platform tenant with billable
    usage, it drafts the month's statement, holds consumers whose ledger
    misses requests telemetry saw (`hold_on_gaps`), finalizes it, and with
    `export` set to stripe, paddle or lago exports each statement to that provider. Usage attributed to
    a
    platform tenant is billed on the tenant's statement. One run per
    account can be queued or running at a time.

    Args:
        idempotency_key (str | Unset):
        body (CreateBillingRunRequest): A month-end billing run. Omitted options bill both
            sources, finalize, do not export, and hold consumers with completeness gaps.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingRunResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateBillingRunRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[BillingRunResponse | Problem]:
    """Bill one ended month for every customer.

     Starts a background run for one UTC calendar month that ended at least
    an hour ago. For every app consumer and platform tenant with billable
    usage, it drafts the month's statement, holds consumers whose ledger
    misses requests telemetry saw (`hold_on_gaps`), finalizes it, and with
    `export` set to stripe, paddle or lago exports each statement to that provider. Usage attributed to
    a
    platform tenant is billed on the tenant's statement. One run per
    account can be queued or running at a time.

    Args:
        idempotency_key (str | Unset):
        body (CreateBillingRunRequest): A month-end billing run. Omitted options bill both
            sources, finalize, do not export, and hold consumers with completeness gaps.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingRunResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreateBillingRunRequest,
    idempotency_key: str | Unset = UNSET,
) -> BillingRunResponse | Problem | None:
    """Bill one ended month for every customer.

     Starts a background run for one UTC calendar month that ended at least
    an hour ago. For every app consumer and platform tenant with billable
    usage, it drafts the month's statement, holds consumers whose ledger
    misses requests telemetry saw (`hold_on_gaps`), finalizes it, and with
    `export` set to stripe, paddle or lago exports each statement to that provider. Usage attributed to
    a
    platform tenant is billed on the tenant's statement. One run per
    account can be queued or running at a time.

    Args:
        idempotency_key (str | Unset):
        body (CreateBillingRunRequest): A month-end billing run. Omitted options bill both
            sources, finalize, do not export, and hold consumers with completeness gaps.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingRunResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
