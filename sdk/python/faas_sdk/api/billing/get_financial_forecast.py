from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.financial_forecast_response import FinancialForecastResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    month: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["month"] = month

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/billing/forecast",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FinancialForecastResponse | Problem | None:
    if response.status_code == 200:
        response_200 = FinancialForecastResponse.from_dict(response.json())

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[FinancialForecastResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    month: str | Unset = UNSET,
) -> Response[FinancialForecastResponse | Problem]:
    """Read usage cost forecasts with coverage and method.

     Requires usage:read and session MFA. Elapsed-time quantity forecasts apply
    the recorded price after the shared allowance. At least one complete day,
    fresh evidence, and unchanged pricing are required. Unavailable forecasts
    contain a reason and omit projected amounts. The overall invoice forecast
    remains unavailable until all bill components have authoritative coverage.
    This endpoint is read-only and never changes workload admission.

    Args:
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FinancialForecastResponse | Problem]
    """

    kwargs = _get_kwargs(
        month=month,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    month: str | Unset = UNSET,
) -> FinancialForecastResponse | Problem | None:
    """Read usage cost forecasts with coverage and method.

     Requires usage:read and session MFA. Elapsed-time quantity forecasts apply
    the recorded price after the shared allowance. At least one complete day,
    fresh evidence, and unchanged pricing are required. Unavailable forecasts
    contain a reason and omit projected amounts. The overall invoice forecast
    remains unavailable until all bill components have authoritative coverage.
    This endpoint is read-only and never changes workload admission.

    Args:
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FinancialForecastResponse | Problem
    """

    return sync_detailed(
        client=client,
        month=month,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    month: str | Unset = UNSET,
) -> Response[FinancialForecastResponse | Problem]:
    """Read usage cost forecasts with coverage and method.

     Requires usage:read and session MFA. Elapsed-time quantity forecasts apply
    the recorded price after the shared allowance. At least one complete day,
    fresh evidence, and unchanged pricing are required. Unavailable forecasts
    contain a reason and omit projected amounts. The overall invoice forecast
    remains unavailable until all bill components have authoritative coverage.
    This endpoint is read-only and never changes workload admission.

    Args:
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FinancialForecastResponse | Problem]
    """

    kwargs = _get_kwargs(
        month=month,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    month: str | Unset = UNSET,
) -> FinancialForecastResponse | Problem | None:
    """Read usage cost forecasts with coverage and method.

     Requires usage:read and session MFA. Elapsed-time quantity forecasts apply
    the recorded price after the shared allowance. At least one complete day,
    fresh evidence, and unchanged pricing are required. Unavailable forecasts
    contain a reason and omit projected amounts. The overall invoice forecast
    remains unavailable until all bill components have authoritative coverage.
    This endpoint is read-only and never changes workload admission.

    Args:
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FinancialForecastResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            month=month,
        )
    ).parsed
