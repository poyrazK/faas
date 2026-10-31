from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.data_api_usage import DataAPIUsage
from ...models.get_app_data_api_usage_granularity import (
    GetAppDataAPIUsageGranularity,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: str | Unset = UNSET,
    granularity: GetAppDataAPIUsageGranularity | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["since"] = since

    json_granularity: str | Unset = UNSET
    if not isinstance(granularity, Unset):
        json_granularity = granularity

    params["granularity"] = json_granularity

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/data-api/usage".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> DataAPIUsage | Problem:
    if response.status_code == 200:
        response_200 = DataAPIUsage.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DataAPIUsage | Problem]:
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
    since: str | Unset = UNSET,
    granularity: GetAppDataAPIUsageGranularity | Unset = UNSET,
) -> Response[DataAPIUsage | Problem]:
    """Read Data API usage

     Usage that the app's Data API runtime reported, per hour or UTC day, with month-to-date totals that
    owner caps compare against. Counts come from the app's own runtime and are owner telemetry, not
    billing records. Apps created before usage reporting, or with it off, have no rows.

    Args:
        slug (str):
        since (str | Unset):
        granularity (GetAppDataAPIUsageGranularity | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataAPIUsage | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        granularity=granularity,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    granularity: GetAppDataAPIUsageGranularity | Unset = UNSET,
) -> DataAPIUsage | Problem | None:
    """Read Data API usage

     Usage that the app's Data API runtime reported, per hour or UTC day, with month-to-date totals that
    owner caps compare against. Counts come from the app's own runtime and are owner telemetry, not
    billing records. Apps created before usage reporting, or with it off, have no rows.

    Args:
        slug (str):
        since (str | Unset):
        granularity (GetAppDataAPIUsageGranularity | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataAPIUsage | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        since=since,
        granularity=granularity,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    granularity: GetAppDataAPIUsageGranularity | Unset = UNSET,
) -> Response[DataAPIUsage | Problem]:
    """Read Data API usage

     Usage that the app's Data API runtime reported, per hour or UTC day, with month-to-date totals that
    owner caps compare against. Counts come from the app's own runtime and are owner telemetry, not
    billing records. Apps created before usage reporting, or with it off, have no rows.

    Args:
        slug (str):
        since (str | Unset):
        granularity (GetAppDataAPIUsageGranularity | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataAPIUsage | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        granularity=granularity,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
    granularity: GetAppDataAPIUsageGranularity | Unset = UNSET,
) -> DataAPIUsage | Problem | None:
    """Read Data API usage

     Usage that the app's Data API runtime reported, per hour or UTC day, with month-to-date totals that
    owner caps compare against. Counts come from the app's own runtime and are owner telemetry, not
    billing records. Apps created before usage reporting, or with it off, have no rows.

    Args:
        slug (str):
        since (str | Unset):
        granularity (GetAppDataAPIUsageGranularity | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataAPIUsage | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
            granularity=granularity,
        )
    ).parsed
