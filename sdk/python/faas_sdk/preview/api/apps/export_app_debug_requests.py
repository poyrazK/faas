from http import HTTPStatus
from io import BytesIO
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.export_app_debug_requests_format import (
    ExportAppDebugRequestsFormat,
)
from ...models.problem import Problem
from ...types import UNSET, File, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: None | str | Unset = UNSET,
    route: None | str | Unset = UNSET,
    format_: ExportAppDebugRequestsFormat | Unset = "ndjson",
    limit: int | None | Unset = 10000,
    deployment_id: None | Unset | UUID = UNSET,
    status: int | None | Unset = UNSET,
    cold_boot: bool | None | Unset = UNSET,
    consumer_id: None | str | Unset = UNSET,
    min_latency_ms: int | None | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since: None | str | Unset
    if isinstance(since, Unset):
        json_since = UNSET
    else:
        json_since = since
    params["since"] = json_since

    json_route: None | str | Unset
    if isinstance(route, Unset):
        json_route = UNSET
    else:
        json_route = route
    params["route"] = json_route

    json_format_: str | Unset = UNSET
    if not isinstance(format_, Unset):
        json_format_ = format_

    params["format"] = json_format_

    json_limit: int | None | Unset
    if isinstance(limit, Unset):
        json_limit = UNSET
    else:
        json_limit = limit
    params["limit"] = json_limit

    json_deployment_id: None | str | Unset
    if isinstance(deployment_id, Unset):
        json_deployment_id = UNSET
    elif isinstance(deployment_id, UUID):
        json_deployment_id = str(deployment_id)
    else:
        json_deployment_id = deployment_id
    params["deployment_id"] = json_deployment_id

    json_status: int | None | Unset
    if isinstance(status, Unset):
        json_status = UNSET
    else:
        json_status = status
    params["status"] = json_status

    json_cold_boot: bool | None | Unset
    if isinstance(cold_boot, Unset):
        json_cold_boot = UNSET
    else:
        json_cold_boot = cold_boot
    params["cold_boot"] = json_cold_boot

    json_consumer_id: None | str | Unset
    if isinstance(consumer_id, Unset):
        json_consumer_id = UNSET
    else:
        json_consumer_id = consumer_id
    params["consumer_id"] = json_consumer_id

    json_min_latency_ms: int | None | Unset
    if isinstance(min_latency_ms, Unset):
        json_min_latency_ms = UNSET
    else:
        json_min_latency_ms = min_latency_ms
    params["min_latency_ms"] = json_min_latency_ms

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/debug/requests/export".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> File | Problem | None:
    if response.status_code == 200:
        response_200 = File(payload=BytesIO(response.content))

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[File | Problem]:
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
    since: None | str | Unset = UNSET,
    route: None | str | Unset = UNSET,
    format_: ExportAppDebugRequestsFormat | Unset = "ndjson",
    limit: int | None | Unset = 10000,
    deployment_id: None | Unset | UUID = UNSET,
    status: int | None | Unset = UNSET,
    cold_boot: bool | None | Unset = UNSET,
    consumer_id: None | str | Unset = UNSET,
    min_latency_ms: int | None | Unset = UNSET,
) -> Response[File | Problem]:
    """Export retained request telemetry.

     Downloads a bounded, metadata-only request-log artifact for the app.
    The default format is newline-delimited JSON (`ndjson`); `csv` is
    available for spreadsheet and support workflows. The export uses the
    same plan retention boundary as the debugger list, never includes
    request bodies, headers, source IPs, or raw span attributes, and is
    capped at 10,000 rows. It takes the request list's filters.
    `X-Faas-Request-Log-Window` and `X-Faas-Request-Log-Retention-Clamped`
    describe the effective window applied to the artifact, and
    `X-Faas-Request-Log-Truncated` says whether the row limit cut it short.
    In CSV, a value that would start a spreadsheet formula is written
    with a leading apostrophe.

    Args:
        slug (str):
        since (None | str | Unset):
        route (None | str | Unset):
        format_ (ExportAppDebugRequestsFormat | Unset):  Default: 'ndjson'.
        limit (int | None | Unset):  Default: 10000.
        deployment_id (None | Unset | UUID):
        status (int | None | Unset):
        cold_boot (bool | None | Unset):
        consumer_id (None | str | Unset):
        min_latency_ms (int | None | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[File | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        route=route,
        format_=format_,
        limit=limit,
        deployment_id=deployment_id,
        status=status,
        cold_boot=cold_boot,
        consumer_id=consumer_id,
        min_latency_ms=min_latency_ms,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: None | str | Unset = UNSET,
    route: None | str | Unset = UNSET,
    format_: ExportAppDebugRequestsFormat | Unset = "ndjson",
    limit: int | None | Unset = 10000,
    deployment_id: None | Unset | UUID = UNSET,
    status: int | None | Unset = UNSET,
    cold_boot: bool | None | Unset = UNSET,
    consumer_id: None | str | Unset = UNSET,
    min_latency_ms: int | None | Unset = UNSET,
) -> File | Problem | None:
    """Export retained request telemetry.

     Downloads a bounded, metadata-only request-log artifact for the app.
    The default format is newline-delimited JSON (`ndjson`); `csv` is
    available for spreadsheet and support workflows. The export uses the
    same plan retention boundary as the debugger list, never includes
    request bodies, headers, source IPs, or raw span attributes, and is
    capped at 10,000 rows. It takes the request list's filters.
    `X-Faas-Request-Log-Window` and `X-Faas-Request-Log-Retention-Clamped`
    describe the effective window applied to the artifact, and
    `X-Faas-Request-Log-Truncated` says whether the row limit cut it short.
    In CSV, a value that would start a spreadsheet formula is written
    with a leading apostrophe.

    Args:
        slug (str):
        since (None | str | Unset):
        route (None | str | Unset):
        format_ (ExportAppDebugRequestsFormat | Unset):  Default: 'ndjson'.
        limit (int | None | Unset):  Default: 10000.
        deployment_id (None | Unset | UUID):
        status (int | None | Unset):
        cold_boot (bool | None | Unset):
        consumer_id (None | str | Unset):
        min_latency_ms (int | None | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        File | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        since=since,
        route=route,
        format_=format_,
        limit=limit,
        deployment_id=deployment_id,
        status=status,
        cold_boot=cold_boot,
        consumer_id=consumer_id,
        min_latency_ms=min_latency_ms,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: None | str | Unset = UNSET,
    route: None | str | Unset = UNSET,
    format_: ExportAppDebugRequestsFormat | Unset = "ndjson",
    limit: int | None | Unset = 10000,
    deployment_id: None | Unset | UUID = UNSET,
    status: int | None | Unset = UNSET,
    cold_boot: bool | None | Unset = UNSET,
    consumer_id: None | str | Unset = UNSET,
    min_latency_ms: int | None | Unset = UNSET,
) -> Response[File | Problem]:
    """Export retained request telemetry.

     Downloads a bounded, metadata-only request-log artifact for the app.
    The default format is newline-delimited JSON (`ndjson`); `csv` is
    available for spreadsheet and support workflows. The export uses the
    same plan retention boundary as the debugger list, never includes
    request bodies, headers, source IPs, or raw span attributes, and is
    capped at 10,000 rows. It takes the request list's filters.
    `X-Faas-Request-Log-Window` and `X-Faas-Request-Log-Retention-Clamped`
    describe the effective window applied to the artifact, and
    `X-Faas-Request-Log-Truncated` says whether the row limit cut it short.
    In CSV, a value that would start a spreadsheet formula is written
    with a leading apostrophe.

    Args:
        slug (str):
        since (None | str | Unset):
        route (None | str | Unset):
        format_ (ExportAppDebugRequestsFormat | Unset):  Default: 'ndjson'.
        limit (int | None | Unset):  Default: 10000.
        deployment_id (None | Unset | UUID):
        status (int | None | Unset):
        cold_boot (bool | None | Unset):
        consumer_id (None | str | Unset):
        min_latency_ms (int | None | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[File | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        route=route,
        format_=format_,
        limit=limit,
        deployment_id=deployment_id,
        status=status,
        cold_boot=cold_boot,
        consumer_id=consumer_id,
        min_latency_ms=min_latency_ms,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: None | str | Unset = UNSET,
    route: None | str | Unset = UNSET,
    format_: ExportAppDebugRequestsFormat | Unset = "ndjson",
    limit: int | None | Unset = 10000,
    deployment_id: None | Unset | UUID = UNSET,
    status: int | None | Unset = UNSET,
    cold_boot: bool | None | Unset = UNSET,
    consumer_id: None | str | Unset = UNSET,
    min_latency_ms: int | None | Unset = UNSET,
) -> File | Problem | None:
    """Export retained request telemetry.

     Downloads a bounded, metadata-only request-log artifact for the app.
    The default format is newline-delimited JSON (`ndjson`); `csv` is
    available for spreadsheet and support workflows. The export uses the
    same plan retention boundary as the debugger list, never includes
    request bodies, headers, source IPs, or raw span attributes, and is
    capped at 10,000 rows. It takes the request list's filters.
    `X-Faas-Request-Log-Window` and `X-Faas-Request-Log-Retention-Clamped`
    describe the effective window applied to the artifact, and
    `X-Faas-Request-Log-Truncated` says whether the row limit cut it short.
    In CSV, a value that would start a spreadsheet formula is written
    with a leading apostrophe.

    Args:
        slug (str):
        since (None | str | Unset):
        route (None | str | Unset):
        format_ (ExportAppDebugRequestsFormat | Unset):  Default: 'ndjson'.
        limit (int | None | Unset):  Default: 10000.
        deployment_id (None | Unset | UUID):
        status (int | None | Unset):
        cold_boot (bool | None | Unset):
        consumer_id (None | str | Unset):
        min_latency_ms (int | None | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        File | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
            route=route,
            format_=format_,
            limit=limit,
            deployment_id=deployment_id,
            status=status,
            cold_boot=cold_boot,
            consumer_id=consumer_id,
            min_latency_ms=min_latency_ms,
        )
    ).parsed
