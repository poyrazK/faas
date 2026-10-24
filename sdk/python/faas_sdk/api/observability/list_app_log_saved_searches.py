from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.log_saved_searches_response import LogSavedSearchesResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/log-searches".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> LogSavedSearchesResponse | Problem:
    if response.status_code == 200:
        response_200 = LogSavedSearchesResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[LogSavedSearchesResponse | Problem]:
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
) -> Response[LogSavedSearchesResponse | Problem]:
    """List an app's saved log searches.

     ADR-20261018083536 internal preview behind `FAAS_LOG_SEARCH_ENABLED=1`.
    Returns the app's saved searches by name. Each holds filters only;
    a search, the dashboard or an alert brings its own window.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[LogSavedSearchesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> LogSavedSearchesResponse | Problem | None:
    """List an app's saved log searches.

     ADR-20261018083536 internal preview behind `FAAS_LOG_SEARCH_ENABLED=1`.
    Returns the app's saved searches by name. Each holds filters only;
    a search, the dashboard or an alert brings its own window.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        LogSavedSearchesResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[LogSavedSearchesResponse | Problem]:
    """List an app's saved log searches.

     ADR-20261018083536 internal preview behind `FAAS_LOG_SEARCH_ENABLED=1`.
    Returns the app's saved searches by name. Each holds filters only;
    a search, the dashboard or an alert brings its own window.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[LogSavedSearchesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> LogSavedSearchesResponse | Problem | None:
    """List an app's saved log searches.

     ADR-20261018083536 internal preview behind `FAAS_LOG_SEARCH_ENABLED=1`.
    Returns the app's saved searches by name. Each holds filters only;
    a search, the dashboard or an alert brings its own window.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        LogSavedSearchesResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
