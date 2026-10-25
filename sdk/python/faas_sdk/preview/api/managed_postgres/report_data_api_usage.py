from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.data_api_usage_report import DataAPIUsageReport
from ...models.data_api_usage_report_response import DataAPIUsageReportResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: DataAPIUsageReport,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/runtime/data-api/usage",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DataAPIUsageReportResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DataAPIUsageReportResponse.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DataAPIUsageReportResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: DataAPIUsageReport,
) -> Response[DataAPIUsageReportResponse | Problem]:
    """Report Data API usage deltas

     Private runtime surface. Requires a short-lived gregale:data-api:usage workload assertion; account,
    app and instance come from it, never from the body. Each report id is applied once, so retried or
    snapshot-duplicated reports add nothing. Hours must be aligned and at most 48 hours old. Owner API
    keys and application JWTs are not accepted.

    Args:
        body (DataAPIUsageReport): Private versioned runtime usage report. A report id names the
            reporting instance and process and is applied once; hours are unique.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataAPIUsageReportResponse | Problem]
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
    client: AuthenticatedClient,
    body: DataAPIUsageReport,
) -> DataAPIUsageReportResponse | Problem | None:
    """Report Data API usage deltas

     Private runtime surface. Requires a short-lived gregale:data-api:usage workload assertion; account,
    app and instance come from it, never from the body. Each report id is applied once, so retried or
    snapshot-duplicated reports add nothing. Hours must be aligned and at most 48 hours old. Owner API
    keys and application JWTs are not accepted.

    Args:
        body (DataAPIUsageReport): Private versioned runtime usage report. A report id names the
            reporting instance and process and is applied once; hours are unique.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataAPIUsageReportResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: DataAPIUsageReport,
) -> Response[DataAPIUsageReportResponse | Problem]:
    """Report Data API usage deltas

     Private runtime surface. Requires a short-lived gregale:data-api:usage workload assertion; account,
    app and instance come from it, never from the body. Each report id is applied once, so retried or
    snapshot-duplicated reports add nothing. Hours must be aligned and at most 48 hours old. Owner API
    keys and application JWTs are not accepted.

    Args:
        body (DataAPIUsageReport): Private versioned runtime usage report. A report id names the
            reporting instance and process and is applied once; hours are unique.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataAPIUsageReportResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: DataAPIUsageReport,
) -> DataAPIUsageReportResponse | Problem | None:
    """Report Data API usage deltas

     Private runtime surface. Requires a short-lived gregale:data-api:usage workload assertion; account,
    app and instance come from it, never from the body. Each report id is applied once, so retried or
    snapshot-duplicated reports add nothing. Hours must be aligned and at most 48 hours old. Owner API
    keys and application JWTs are not accepted.

    Args:
        body (DataAPIUsageReport): Private versioned runtime usage report. A report id names the
            reporting instance and process and is applied once; hours are unique.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataAPIUsageReportResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
