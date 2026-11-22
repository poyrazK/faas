from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    route: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/upload-routes/{route}".format(
            slug=quote(str(slug), safe=""),
            route=quote(str(route), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    route: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Delete an authenticated upload route

     Stops new uploads immediately. Existing objects are not deleted.

    Args:
        slug (str):
        route (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        route=route,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    route: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Delete an authenticated upload route

     Stops new uploads immediately. Existing objects are not deleted.

    Args:
        slug (str):
        route (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        route=route,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    route: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Delete an authenticated upload route

     Stops new uploads immediately. Existing objects are not deleted.

    Args:
        slug (str):
        route (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        route=route,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    route: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Delete an authenticated upload route

     Stops new uploads immediately. Existing objects are not deleted.

    Args:
        slug (str):
        route (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            route=route,
            client=client,
        )
    ).parsed
