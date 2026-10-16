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

     Account-scoped metadata only; requires managed PostgreSQL read permission. Does not wake SQL,
    reserve connections or change admission. Current adapters have no verified usable-capacity
    observation and return unknown with canonical pool estimates. Database snapshots are independent.

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

     Account-scoped metadata only; requires managed PostgreSQL read permission. Does not wake SQL,
    reserve connections or change admission. Current adapters have no verified usable-capacity
    observation and return unknown with canonical pool estimates. Database snapshots are independent.

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

     Account-scoped metadata only; requires managed PostgreSQL read permission. Does not wake SQL,
    reserve connections or change admission. Current adapters have no verified usable-capacity
    observation and return unknown with canonical pool estimates. Database snapshots are independent.

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

     Account-scoped metadata only; requires managed PostgreSQL read permission. Does not wake SQL,
    reserve connections or change admission. Current adapters have no verified usable-capacity
    observation and return unknown with canonical pool estimates. Database snapshots are independent.

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
