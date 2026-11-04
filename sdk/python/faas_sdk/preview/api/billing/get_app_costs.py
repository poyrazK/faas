from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_costs_response import AppCostsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    month: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["month"] = month

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/costs".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppCostsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppCostsResponse.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppCostsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    month: str | Unset = UNSET,
) -> Response[AppCostsResponse | Problem]:
    """Read retained usage costs attributed to one application.

     Requires usage:read and current application access. App-agent credentials may
    opt into usage:read for their bound app; account billing remains inaccessible.
    Uses the native billing subject and the canonical allocation of included
    usage. Excludes other applications, account totals, invoices, forecasts,
    and account-wide unpriced or nonbillable quantities. Sampling windows remain
    explicit; per-app quantity coverage and a complete bill estimate are unknown.
    All monetary amounts use integer millicents. This endpoint has no writes.

    Args:
        slug (str):
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppCostsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        month=month,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    month: str | Unset = UNSET,
) -> AppCostsResponse | Problem | None:
    """Read retained usage costs attributed to one application.

     Requires usage:read and current application access. App-agent credentials may
    opt into usage:read for their bound app; account billing remains inaccessible.
    Uses the native billing subject and the canonical allocation of included
    usage. Excludes other applications, account totals, invoices, forecasts,
    and account-wide unpriced or nonbillable quantities. Sampling windows remain
    explicit; per-app quantity coverage and a complete bill estimate are unknown.
    All monetary amounts use integer millicents. This endpoint has no writes.

    Args:
        slug (str):
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppCostsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        month=month,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    month: str | Unset = UNSET,
) -> Response[AppCostsResponse | Problem]:
    """Read retained usage costs attributed to one application.

     Requires usage:read and current application access. App-agent credentials may
    opt into usage:read for their bound app; account billing remains inaccessible.
    Uses the native billing subject and the canonical allocation of included
    usage. Excludes other applications, account totals, invoices, forecasts,
    and account-wide unpriced or nonbillable quantities. Sampling windows remain
    explicit; per-app quantity coverage and a complete bill estimate are unknown.
    All monetary amounts use integer millicents. This endpoint has no writes.

    Args:
        slug (str):
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppCostsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        month=month,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    month: str | Unset = UNSET,
) -> AppCostsResponse | Problem | None:
    """Read retained usage costs attributed to one application.

     Requires usage:read and current application access. App-agent credentials may
    opt into usage:read for their bound app; account billing remains inaccessible.
    Uses the native billing subject and the canonical allocation of included
    usage. Excludes other applications, account totals, invoices, forecasts,
    and account-wide unpriced or nonbillable quantities. Sampling windows remain
    explicit; per-app quantity coverage and a complete bill estimate are unknown.
    All monetary amounts use integer millicents. This endpoint has no writes.

    Args:
        slug (str):
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppCostsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            month=month,
        )
    ).parsed
