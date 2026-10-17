from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.data_api_connection_budgets import DataAPIConnectionBudgets
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:
    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/data-api/connection-budget".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DataAPIConnectionBudgets | Problem:
    if response.status_code == 200:
        response_200 = DataAPIConnectionBudgets.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DataAPIConnectionBudgets | Problem]:
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
) -> Response[DataAPIConnectionBudgets | Problem]:
    """Inspect shared database budgets for Data API bindings

     Lists independently sampled planning reports for the app's shared managed Data API databases,
    including other bindings in the same account. Requires managed PostgreSQL read permission. Reads
    metadata without waking SQL or changing admission; current adapters retain unknown capacity.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataAPIConnectionBudgets | Problem]
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
) -> DataAPIConnectionBudgets | Problem | None:
    """Inspect shared database budgets for Data API bindings

     Lists independently sampled planning reports for the app's shared managed Data API databases,
    including other bindings in the same account. Requires managed PostgreSQL read permission. Reads
    metadata without waking SQL or changing admission; current adapters retain unknown capacity.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataAPIConnectionBudgets | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DataAPIConnectionBudgets | Problem]:
    """Inspect shared database budgets for Data API bindings

     Lists independently sampled planning reports for the app's shared managed Data API databases,
    including other bindings in the same account. Requires managed PostgreSQL read permission. Reads
    metadata without waking SQL or changing admission; current adapters retain unknown capacity.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataAPIConnectionBudgets | Problem]
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
) -> DataAPIConnectionBudgets | Problem | None:
    """Inspect shared database budgets for Data API bindings

     Lists independently sampled planning reports for the app's shared managed Data API databases,
    including other bindings in the same account. Requires managed PostgreSQL read permission. Reads
    metadata without waking SQL or changing admission; current adapters retain unknown capacity.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataAPIConnectionBudgets | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
