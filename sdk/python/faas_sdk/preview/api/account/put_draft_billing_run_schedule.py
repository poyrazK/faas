from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.billing_run_schedule_response import BillingRunScheduleResponse
from ...models.problem import Problem
from ...models.put_draft_billing_run_schedule_request import PutDraftBillingRunScheduleRequest
from ...types import Response


def _get_kwargs(
    *,
    body: PutDraftBillingRunScheduleRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/account/billing-run-schedule/draft",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BillingRunScheduleResponse | Problem | None:
    if response.status_code == 200:
        response_200 = BillingRunScheduleResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[BillingRunScheduleResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PutDraftBillingRunScheduleRequest,
) -> Response[BillingRunScheduleResponse | Problem]:
    """Create a draft-only monthly schedule or change its delay.

     Creates the schedule only if absent, or adjusts the reviewed version while preserving the next month
    and billing options.

    Args:
        body (PutDraftBillingRunScheduleRequest): Timing and optional reviewed version for a
            monthly draft schedule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingRunScheduleResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: PutDraftBillingRunScheduleRequest,
) -> BillingRunScheduleResponse | Problem | None:
    """Create a draft-only monthly schedule or change its delay.

     Creates the schedule only if absent, or adjusts the reviewed version while preserving the next month
    and billing options.

    Args:
        body (PutDraftBillingRunScheduleRequest): Timing and optional reviewed version for a
            monthly draft schedule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingRunScheduleResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PutDraftBillingRunScheduleRequest,
) -> Response[BillingRunScheduleResponse | Problem]:
    """Create a draft-only monthly schedule or change its delay.

     Creates the schedule only if absent, or adjusts the reviewed version while preserving the next month
    and billing options.

    Args:
        body (PutDraftBillingRunScheduleRequest): Timing and optional reviewed version for a
            monthly draft schedule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BillingRunScheduleResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: PutDraftBillingRunScheduleRequest,
) -> BillingRunScheduleResponse | Problem | None:
    """Create a draft-only monthly schedule or change its delay.

     Creates the schedule only if absent, or adjusts the reviewed version while preserving the next month
    and billing options.

    Args:
        body (PutDraftBillingRunScheduleRequest): Timing and optional reviewed version for a
            monthly draft schedule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BillingRunScheduleResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
