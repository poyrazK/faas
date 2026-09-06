import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.data_upstream_history_response import DataUpstreamHistoryResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    from_: datetime.datetime | Unset = UNSET,
    to: datetime.datetime | Unset = UNSET,
    bucket: str | Unset = "5m",
    region: str | Unset = UNSET,
    deployment_scope: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_from_: str | Unset = UNSET
    if not isinstance(from_, Unset):
        json_from_ = from_.isoformat()
    params["from"] = json_from_

    json_to: str | Unset = UNSET
    if not isinstance(to, Unset):
        json_to = to.isoformat()
    params["to"] = json_to

    params["bucket"] = bucket

    params["region"] = region

    params["deployment_scope"] = deployment_scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/upstreams/history".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | list[DataUpstreamHistoryResponse] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = DataUpstreamHistoryResponse.from_dict(response_200_item_data)

            response_200.append(response_200_item)

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[Problem | list[DataUpstreamHistoryResponse]]:
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
    from_: datetime.datetime | Unset = UNSET,
    to: datetime.datetime | Unset = UNSET,
    bucket: str | Unset = "5m",
    region: str | Unset = UNSET,
    deployment_scope: str | Unset = UNSET,
) -> Response[Problem | list[DataUpstreamHistoryResponse]]:
    """Get historical data-upstream probe metrics.

     Returns one time series per captured upstream and probe region.
    Each bucket contains p50/p95 successful RTTs and the total number
    of probes, including failures. The query is bounded to the probe
    retention window and is aggregated server-side; raw probe rows and
    plaintext hosts are never returned.

    Args:
        slug (str):
        from_ (datetime.datetime | Unset):
        to (datetime.datetime | Unset):
        bucket (str | Unset):  Default: '5m'.
        region (str | Unset):
        deployment_scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[DataUpstreamHistoryResponse]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        from_=from_,
        to=to,
        bucket=bucket,
        region=region,
        deployment_scope=deployment_scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    from_: datetime.datetime | Unset = UNSET,
    to: datetime.datetime | Unset = UNSET,
    bucket: str | Unset = "5m",
    region: str | Unset = UNSET,
    deployment_scope: str | Unset = UNSET,
) -> Problem | list[DataUpstreamHistoryResponse] | None:
    """Get historical data-upstream probe metrics.

     Returns one time series per captured upstream and probe region.
    Each bucket contains p50/p95 successful RTTs and the total number
    of probes, including failures. The query is bounded to the probe
    retention window and is aggregated server-side; raw probe rows and
    plaintext hosts are never returned.

    Args:
        slug (str):
        from_ (datetime.datetime | Unset):
        to (datetime.datetime | Unset):
        bucket (str | Unset):  Default: '5m'.
        region (str | Unset):
        deployment_scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[DataUpstreamHistoryResponse]
    """

    return sync_detailed(
        slug=slug,
        client=client,
        from_=from_,
        to=to,
        bucket=bucket,
        region=region,
        deployment_scope=deployment_scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    from_: datetime.datetime | Unset = UNSET,
    to: datetime.datetime | Unset = UNSET,
    bucket: str | Unset = "5m",
    region: str | Unset = UNSET,
    deployment_scope: str | Unset = UNSET,
) -> Response[Problem | list[DataUpstreamHistoryResponse]]:
    """Get historical data-upstream probe metrics.

     Returns one time series per captured upstream and probe region.
    Each bucket contains p50/p95 successful RTTs and the total number
    of probes, including failures. The query is bounded to the probe
    retention window and is aggregated server-side; raw probe rows and
    plaintext hosts are never returned.

    Args:
        slug (str):
        from_ (datetime.datetime | Unset):
        to (datetime.datetime | Unset):
        bucket (str | Unset):  Default: '5m'.
        region (str | Unset):
        deployment_scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[DataUpstreamHistoryResponse]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        from_=from_,
        to=to,
        bucket=bucket,
        region=region,
        deployment_scope=deployment_scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    from_: datetime.datetime | Unset = UNSET,
    to: datetime.datetime | Unset = UNSET,
    bucket: str | Unset = "5m",
    region: str | Unset = UNSET,
    deployment_scope: str | Unset = UNSET,
) -> Problem | list[DataUpstreamHistoryResponse] | None:
    """Get historical data-upstream probe metrics.

     Returns one time series per captured upstream and probe region.
    Each bucket contains p50/p95 successful RTTs and the total number
    of probes, including failures. The query is bounded to the probe
    retention window and is aggregated server-side; raw probe rows and
    plaintext hosts are never returned.

    Args:
        slug (str):
        from_ (datetime.datetime | Unset):
        to (datetime.datetime | Unset):
        bucket (str | Unset):  Default: '5m'.
        region (str | Unset):
        deployment_scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[DataUpstreamHistoryResponse]
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            from_=from_,
            to=to,
            bucket=bucket,
            region=region,
            deployment_scope=deployment_scope,
        )
    ).parsed
