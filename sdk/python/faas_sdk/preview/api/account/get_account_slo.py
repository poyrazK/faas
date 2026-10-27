from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_slo_response import AccountSLOResponse
from ...models.get_account_slo_window import GetAccountSLOWindow
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    window: GetAccountSLOWindow | Unset = "24h",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_window: str | Unset = UNSET
    if not isinstance(window, Unset):
        json_window = window

    params["window"] = json_window

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/slo",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountSLOResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AccountSLOResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AccountSLOResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    window: GetAccountSLOWindow | Unset = "24h",
) -> Response[AccountSLOResponse | Problem]:
    r"""Account-wide SLO rollup (issue

     Flat scalar SLO rollup for the authenticated account. The
    same wire shape as the per-app endpoint without the
    `app_id` / `app_slug` fields — the rollup is sum-based
    across every app the account owns. `instance_hours` and
    `gb_hours` are summed from `usage_minutes` over the
    window; the per-app equivalent is the explicit
    `/v1/apps/{slug}/slo` endpoint.

    `window` is the same closed vocabulary as the per-app
    endpoint: `1h` | `24h` (default) | `7d`. Auth chain:
    `usage:read` scope + MFA. The SLO rollup is a Hobby+
    observability surface; Free accounts receive 402.

    On Prometheus failure the endpoint returns 200 with
    zeroed fields and `source: \"degraded: <reason>\"`. When
    Postgres is down but the PromQL pass succeeded, only
    `instance_hours` / `gb_hours` are zeroed and `source` is
    `\"degraded: postgres unavailable\"`.

    Args:
        window (GetAccountSLOWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountSLOResponse | Problem]
    """

    kwargs = _get_kwargs(
        window=window,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    window: GetAccountSLOWindow | Unset = "24h",
) -> AccountSLOResponse | Problem | None:
    r"""Account-wide SLO rollup (issue

     Flat scalar SLO rollup for the authenticated account. The
    same wire shape as the per-app endpoint without the
    `app_id` / `app_slug` fields — the rollup is sum-based
    across every app the account owns. `instance_hours` and
    `gb_hours` are summed from `usage_minutes` over the
    window; the per-app equivalent is the explicit
    `/v1/apps/{slug}/slo` endpoint.

    `window` is the same closed vocabulary as the per-app
    endpoint: `1h` | `24h` (default) | `7d`. Auth chain:
    `usage:read` scope + MFA. The SLO rollup is a Hobby+
    observability surface; Free accounts receive 402.

    On Prometheus failure the endpoint returns 200 with
    zeroed fields and `source: \"degraded: <reason>\"`. When
    Postgres is down but the PromQL pass succeeded, only
    `instance_hours` / `gb_hours` are zeroed and `source` is
    `\"degraded: postgres unavailable\"`.

    Args:
        window (GetAccountSLOWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountSLOResponse | Problem
    """

    return sync_detailed(
        client=client,
        window=window,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    window: GetAccountSLOWindow | Unset = "24h",
) -> Response[AccountSLOResponse | Problem]:
    r"""Account-wide SLO rollup (issue

     Flat scalar SLO rollup for the authenticated account. The
    same wire shape as the per-app endpoint without the
    `app_id` / `app_slug` fields — the rollup is sum-based
    across every app the account owns. `instance_hours` and
    `gb_hours` are summed from `usage_minutes` over the
    window; the per-app equivalent is the explicit
    `/v1/apps/{slug}/slo` endpoint.

    `window` is the same closed vocabulary as the per-app
    endpoint: `1h` | `24h` (default) | `7d`. Auth chain:
    `usage:read` scope + MFA. The SLO rollup is a Hobby+
    observability surface; Free accounts receive 402.

    On Prometheus failure the endpoint returns 200 with
    zeroed fields and `source: \"degraded: <reason>\"`. When
    Postgres is down but the PromQL pass succeeded, only
    `instance_hours` / `gb_hours` are zeroed and `source` is
    `\"degraded: postgres unavailable\"`.

    Args:
        window (GetAccountSLOWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountSLOResponse | Problem]
    """

    kwargs = _get_kwargs(
        window=window,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    window: GetAccountSLOWindow | Unset = "24h",
) -> AccountSLOResponse | Problem | None:
    r"""Account-wide SLO rollup (issue

     Flat scalar SLO rollup for the authenticated account. The
    same wire shape as the per-app endpoint without the
    `app_id` / `app_slug` fields — the rollup is sum-based
    across every app the account owns. `instance_hours` and
    `gb_hours` are summed from `usage_minutes` over the
    window; the per-app equivalent is the explicit
    `/v1/apps/{slug}/slo` endpoint.

    `window` is the same closed vocabulary as the per-app
    endpoint: `1h` | `24h` (default) | `7d`. Auth chain:
    `usage:read` scope + MFA. The SLO rollup is a Hobby+
    observability surface; Free accounts receive 402.

    On Prometheus failure the endpoint returns 200 with
    zeroed fields and `source: \"degraded: <reason>\"`. When
    Postgres is down but the PromQL pass succeeded, only
    `instance_hours` / `gb_hours` are zeroed and `source` is
    `\"degraded: postgres unavailable\"`.

    Args:
        window (GetAccountSLOWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountSLOResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            window=window,
        )
    ).parsed
