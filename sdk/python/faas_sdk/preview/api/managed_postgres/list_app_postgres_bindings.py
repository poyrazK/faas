from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.app_postgres_binding_list import AppPostgresBindingList
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/postgres-bindings".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppPostgresBindingList | Problem:
    if response.status_code == 200:
        response_200 = AppPostgresBindingList.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppPostgresBindingList | Problem]:
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
) -> Response[AppPostgresBindingList | Problem]:
    """List an app's managed PostgreSQL bindings

     Lists the app's live managed PostgreSQL bindings on every database it
    is bound to, including organization databases and project environment
    copies. Deleting the app is refused while a binding listed here is not
    being deleted. Bindings on an environment copy are removed by tearing
    down that environment.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppPostgresBindingList | Problem]
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
) -> AppPostgresBindingList | Problem | None:
    """List an app's managed PostgreSQL bindings

     Lists the app's live managed PostgreSQL bindings on every database it
    is bound to, including organization databases and project environment
    copies. Deleting the app is refused while a binding listed here is not
    being deleted. Bindings on an environment copy are removed by tearing
    down that environment.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppPostgresBindingList | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppPostgresBindingList | Problem]:
    """List an app's managed PostgreSQL bindings

     Lists the app's live managed PostgreSQL bindings on every database it
    is bound to, including organization databases and project environment
    copies. Deleting the app is refused while a binding listed here is not
    being deleted. Bindings on an environment copy are removed by tearing
    down that environment.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppPostgresBindingList | Problem]
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
) -> AppPostgresBindingList | Problem | None:
    """List an app's managed PostgreSQL bindings

     Lists the app's live managed PostgreSQL bindings on every database it
    is bound to, including organization databases and project environment
    copies. Deleting the app is refused while a binding listed here is not
    being deleted. Bindings on an environment copy are removed by tearing
    down that environment.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppPostgresBindingList | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
