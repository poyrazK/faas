from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.custom_metric_series_response import CustomMetricSeriesResponse
from ...models.get_custom_metric_series_range import GetCustomMetricSeriesRange
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    name: str,
    *,
    range_: GetCustomMetricSeriesRange | Unset = "24h",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_range_: str | Unset = UNSET
    if not isinstance(range_, Unset):
        json_range_ = range_

    params["range"] = json_range_

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/custom-metrics/{name}/series".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CustomMetricSeriesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CustomMetricSeriesResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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
) -> Response[CustomMetricSeriesResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    range_: GetCustomMetricSeriesRange | Unset = "24h",
) -> Response[CustomMetricSeriesResponse | Problem]:
    r"""Read a custom metric's history (ADR-745)

     ADR-745 internal preview, enabled by `FAAS_CUSTOM_METRIC_HISTORY_ENABLED=1`;
    otherwise 503 `custom_metric_history_unavailable`.

    Returns the values Prometheus recorded from pushed custom metrics over
    `range` (`1h`, `6h`, `24h` default, `7d`, `15d`). Only fresh pushes are
    recorded, so a gap means nothing was pushed then. Prometheus failure
    returns 200 with `source: \"degraded: <reason>\"` and null `points`.

    Args:
        slug (str):
        name (str):
        range_ (GetCustomMetricSeriesRange | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CustomMetricSeriesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        range_=range_,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    range_: GetCustomMetricSeriesRange | Unset = "24h",
) -> CustomMetricSeriesResponse | Problem | None:
    r"""Read a custom metric's history (ADR-745)

     ADR-745 internal preview, enabled by `FAAS_CUSTOM_METRIC_HISTORY_ENABLED=1`;
    otherwise 503 `custom_metric_history_unavailable`.

    Returns the values Prometheus recorded from pushed custom metrics over
    `range` (`1h`, `6h`, `24h` default, `7d`, `15d`). Only fresh pushes are
    recorded, so a gap means nothing was pushed then. Prometheus failure
    returns 200 with `source: \"degraded: <reason>\"` and null `points`.

    Args:
        slug (str):
        name (str):
        range_ (GetCustomMetricSeriesRange | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CustomMetricSeriesResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        range_=range_,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    range_: GetCustomMetricSeriesRange | Unset = "24h",
) -> Response[CustomMetricSeriesResponse | Problem]:
    r"""Read a custom metric's history (ADR-745)

     ADR-745 internal preview, enabled by `FAAS_CUSTOM_METRIC_HISTORY_ENABLED=1`;
    otherwise 503 `custom_metric_history_unavailable`.

    Returns the values Prometheus recorded from pushed custom metrics over
    `range` (`1h`, `6h`, `24h` default, `7d`, `15d`). Only fresh pushes are
    recorded, so a gap means nothing was pushed then. Prometheus failure
    returns 200 with `source: \"degraded: <reason>\"` and null `points`.

    Args:
        slug (str):
        name (str):
        range_ (GetCustomMetricSeriesRange | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CustomMetricSeriesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        range_=range_,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    range_: GetCustomMetricSeriesRange | Unset = "24h",
) -> CustomMetricSeriesResponse | Problem | None:
    r"""Read a custom metric's history (ADR-745)

     ADR-745 internal preview, enabled by `FAAS_CUSTOM_METRIC_HISTORY_ENABLED=1`;
    otherwise 503 `custom_metric_history_unavailable`.

    Returns the values Prometheus recorded from pushed custom metrics over
    `range` (`1h`, `6h`, `24h` default, `7d`, `15d`). Only fresh pushes are
    recorded, so a gap means nothing was pushed then. Prometheus failure
    returns 200 with `source: \"degraded: <reason>\"` and null `points`.

    Args:
        slug (str):
        name (str):
        range_ (GetCustomMetricSeriesRange | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CustomMetricSeriesResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            range_=range_,
        )
    ).parsed
