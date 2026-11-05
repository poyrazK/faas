from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.financial_costs_response import FinancialCostsResponse
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
        "url": "/v1/billing/costs",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FinancialCostsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = FinancialCostsResponse.from_dict(response.json())

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
) -> Response[FinancialCostsResponse | Problem]:
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
) -> Response[FinancialCostsResponse | Problem]:
    """Read attributable usage costs and historical price contracts.

     Requires usage:read and session MFA. Uses retained account-owned evidence
    and immutable price versions. Applies a single shared monthly allowance;
    when the plan changes, the largest recorded grant is retained and shared
    proportionally across versions. Known usage amounts use integer millicents.
    Missing samples and historical prices are explicit coverage gaps.
    The reported compute/interface-egress scope excludes other bill components.
    Stored provider invoices are separate facts and are not automatically
    reconciled to this usage ledger. The UTC usage month and provider invoice
    periods can differ. At most 10000 allocations are returned; larger reports
    fail without returning truncated totals. This endpoint has no writes.

    Args:
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FinancialCostsResponse | Problem]
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
) -> FinancialCostsResponse | Problem | None:
    """Read attributable usage costs and historical price contracts.

     Requires usage:read and session MFA. Uses retained account-owned evidence
    and immutable price versions. Applies a single shared monthly allowance;
    when the plan changes, the largest recorded grant is retained and shared
    proportionally across versions. Known usage amounts use integer millicents.
    Missing samples and historical prices are explicit coverage gaps.
    The reported compute/interface-egress scope excludes other bill components.
    Stored provider invoices are separate facts and are not automatically
    reconciled to this usage ledger. The UTC usage month and provider invoice
    periods can differ. At most 10000 allocations are returned; larger reports
    fail without returning truncated totals. This endpoint has no writes.

    Args:
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FinancialCostsResponse | Problem
    """

    return sync_detailed(
        client=client,
        month=month,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    month: str | Unset = UNSET,
) -> Response[FinancialCostsResponse | Problem]:
    """Read attributable usage costs and historical price contracts.

     Requires usage:read and session MFA. Uses retained account-owned evidence
    and immutable price versions. Applies a single shared monthly allowance;
    when the plan changes, the largest recorded grant is retained and shared
    proportionally across versions. Known usage amounts use integer millicents.
    Missing samples and historical prices are explicit coverage gaps.
    The reported compute/interface-egress scope excludes other bill components.
    Stored provider invoices are separate facts and are not automatically
    reconciled to this usage ledger. The UTC usage month and provider invoice
    periods can differ. At most 10000 allocations are returned; larger reports
    fail without returning truncated totals. This endpoint has no writes.

    Args:
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FinancialCostsResponse | Problem]
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
) -> FinancialCostsResponse | Problem | None:
    """Read attributable usage costs and historical price contracts.

     Requires usage:read and session MFA. Uses retained account-owned evidence
    and immutable price versions. Applies a single shared monthly allowance;
    when the plan changes, the largest recorded grant is retained and shared
    proportionally across versions. Known usage amounts use integer millicents.
    Missing samples and historical prices are explicit coverage gaps.
    The reported compute/interface-egress scope excludes other bill components.
    Stored provider invoices are separate facts and are not automatically
    reconciled to this usage ledger. The UTC usage month and provider invoice
    periods can differ. At most 10000 allocations are returned; larger reports
    fail without returning truncated totals. This endpoint has no writes.

    Args:
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FinancialCostsResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            month=month,
        )
    ).parsed
