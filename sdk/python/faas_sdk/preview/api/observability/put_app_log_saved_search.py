from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.log_saved_search import LogSavedSearch
from ...models.log_saved_search_request import LogSavedSearchRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    *,
    body: LogSavedSearchRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/log-searches/{name}".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> LogSavedSearch | Problem:
    if response.status_code == 200:
        response_200 = LogSavedSearch.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[LogSavedSearch | Problem]:
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
    body: LogSavedSearchRequest,
) -> Response[LogSavedSearch | Problem]:
    """Create a saved log search or replace its filters.

     Stores the query canonically: only source, level, status, route,
    deployment_id, request_id, trace_id and q, one value each. An app
    keeps at most 25 saved searches; replacing one never counts.

    Args:
        slug (str):
        name (str):
        body (LogSavedSearchRequest): Body of a saved log search create or replace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[LogSavedSearch | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
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
    body: LogSavedSearchRequest,
) -> LogSavedSearch | Problem | None:
    """Create a saved log search or replace its filters.

     Stores the query canonically: only source, level, status, route,
    deployment_id, request_id, trace_id and q, one value each. An app
    keeps at most 25 saved searches; replacing one never counts.

    Args:
        slug (str):
        name (str):
        body (LogSavedSearchRequest): Body of a saved log search create or replace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        LogSavedSearch | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: LogSavedSearchRequest,
) -> Response[LogSavedSearch | Problem]:
    """Create a saved log search or replace its filters.

     Stores the query canonically: only source, level, status, route,
    deployment_id, request_id, trace_id and q, one value each. An app
    keeps at most 25 saved searches; replacing one never counts.

    Args:
        slug (str):
        name (str):
        body (LogSavedSearchRequest): Body of a saved log search create or replace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[LogSavedSearch | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: LogSavedSearchRequest,
) -> LogSavedSearch | Problem | None:
    """Create a saved log search or replace its filters.

     Stores the query canonically: only source, level, status, route,
    deployment_id, request_id, trace_id and q, one value each. An app
    keeps at most 25 saved searches; replacing one never counts.

    Args:
        slug (str):
        name (str):
        body (LogSavedSearchRequest): Body of a saved log search create or replace.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        LogSavedSearch | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
        )
    ).parsed
