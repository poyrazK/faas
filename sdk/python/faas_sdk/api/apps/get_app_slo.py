from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_slo_response import AppSLOResponse
from ...models.get_app_slo_window import GetAppSLOWindow
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    window: GetAppSLOWindow | Unset = "24h",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_window: str | Unset = UNSET
    if not isinstance(window, Unset):
        json_window = window

    params["window"] = json_window

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/slo".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppSLOResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppSLOResponse.from_dict(response.json())

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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppSLOResponse | Problem]:
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
    window: GetAppSLOWindow | Unset = "24h",
) -> Response[AppSLOResponse | Problem]:
    r"""Per-app customer-facing SLO panel (issue

     Closed-set windowed SLO panel for one app — the
    customer-facing equivalent of AWS CloudWatch
    per-function / GCP Cloud Run per-service. Distinct from
    `GET /v1/apps/{slug}/metrics` (issue #273 / ADR-042) which
    is the 5m-window dashboard panel. The /slo surface is the
    \"yesterday's SLO\" / \"this week's SLO\" summary, with the
    customer-facing SLO signals co-located with the
    billing-derivable `instance_hours` / `gb_hours` fields.

    The `window` parameter is a closed vocabulary, a strict
    subset of the /metrics range vocabulary:

      `1h` | `24h` (default) | `7d`

    `wake_queue_p95_ms` is the FLEET p95
    (`gateway_wake_queue_wait_seconds` is unlabeled). On
    Prometheus failure the endpoint returns 200 with zeroed
    fields and `source: \"degraded: <reason>\"`, matching the
    public status page contract. When Postgres is down but
    the PromQL pass succeeded, only `instance_hours` /
    `gb_hours` are zeroed and `source` is
    `\"degraded: postgres unavailable\"`.

    This is a Hobby+ per-app observability surface. Free
    accounts receive 402 before the app slug is resolved.

    Args:
        slug (str):
        window (GetAppSLOWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppSLOResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        window=window,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    window: GetAppSLOWindow | Unset = "24h",
) -> AppSLOResponse | Problem | None:
    r"""Per-app customer-facing SLO panel (issue

     Closed-set windowed SLO panel for one app — the
    customer-facing equivalent of AWS CloudWatch
    per-function / GCP Cloud Run per-service. Distinct from
    `GET /v1/apps/{slug}/metrics` (issue #273 / ADR-042) which
    is the 5m-window dashboard panel. The /slo surface is the
    \"yesterday's SLO\" / \"this week's SLO\" summary, with the
    customer-facing SLO signals co-located with the
    billing-derivable `instance_hours` / `gb_hours` fields.

    The `window` parameter is a closed vocabulary, a strict
    subset of the /metrics range vocabulary:

      `1h` | `24h` (default) | `7d`

    `wake_queue_p95_ms` is the FLEET p95
    (`gateway_wake_queue_wait_seconds` is unlabeled). On
    Prometheus failure the endpoint returns 200 with zeroed
    fields and `source: \"degraded: <reason>\"`, matching the
    public status page contract. When Postgres is down but
    the PromQL pass succeeded, only `instance_hours` /
    `gb_hours` are zeroed and `source` is
    `\"degraded: postgres unavailable\"`.

    This is a Hobby+ per-app observability surface. Free
    accounts receive 402 before the app slug is resolved.

    Args:
        slug (str):
        window (GetAppSLOWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppSLOResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        window=window,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    window: GetAppSLOWindow | Unset = "24h",
) -> Response[AppSLOResponse | Problem]:
    r"""Per-app customer-facing SLO panel (issue

     Closed-set windowed SLO panel for one app — the
    customer-facing equivalent of AWS CloudWatch
    per-function / GCP Cloud Run per-service. Distinct from
    `GET /v1/apps/{slug}/metrics` (issue #273 / ADR-042) which
    is the 5m-window dashboard panel. The /slo surface is the
    \"yesterday's SLO\" / \"this week's SLO\" summary, with the
    customer-facing SLO signals co-located with the
    billing-derivable `instance_hours` / `gb_hours` fields.

    The `window` parameter is a closed vocabulary, a strict
    subset of the /metrics range vocabulary:

      `1h` | `24h` (default) | `7d`

    `wake_queue_p95_ms` is the FLEET p95
    (`gateway_wake_queue_wait_seconds` is unlabeled). On
    Prometheus failure the endpoint returns 200 with zeroed
    fields and `source: \"degraded: <reason>\"`, matching the
    public status page contract. When Postgres is down but
    the PromQL pass succeeded, only `instance_hours` /
    `gb_hours` are zeroed and `source` is
    `\"degraded: postgres unavailable\"`.

    This is a Hobby+ per-app observability surface. Free
    accounts receive 402 before the app slug is resolved.

    Args:
        slug (str):
        window (GetAppSLOWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppSLOResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        window=window,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    window: GetAppSLOWindow | Unset = "24h",
) -> AppSLOResponse | Problem | None:
    r"""Per-app customer-facing SLO panel (issue

     Closed-set windowed SLO panel for one app — the
    customer-facing equivalent of AWS CloudWatch
    per-function / GCP Cloud Run per-service. Distinct from
    `GET /v1/apps/{slug}/metrics` (issue #273 / ADR-042) which
    is the 5m-window dashboard panel. The /slo surface is the
    \"yesterday's SLO\" / \"this week's SLO\" summary, with the
    customer-facing SLO signals co-located with the
    billing-derivable `instance_hours` / `gb_hours` fields.

    The `window` parameter is a closed vocabulary, a strict
    subset of the /metrics range vocabulary:

      `1h` | `24h` (default) | `7d`

    `wake_queue_p95_ms` is the FLEET p95
    (`gateway_wake_queue_wait_seconds` is unlabeled). On
    Prometheus failure the endpoint returns 200 with zeroed
    fields and `source: \"degraded: <reason>\"`, matching the
    public status page contract. When Postgres is down but
    the PromQL pass succeeded, only `instance_hours` /
    `gb_hours` are zeroed and `source` is
    `\"degraded: postgres unavailable\"`.

    This is a Hobby+ per-app observability surface. Free
    accounts receive 402 before the app slug is resolved.

    Args:
        slug (str):
        window (GetAppSLOWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppSLOResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            window=window,
        )
    ).parsed
