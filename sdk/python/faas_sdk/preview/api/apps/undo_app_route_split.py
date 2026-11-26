from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_split import RouteSplit
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/route-splits/{id}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | Problem | RouteSplit | None:
    if response.status_code == 200:
        response_200 = RouteSplit.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | Problem | RouteSplit]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem | RouteSplit]:
    """Reverse a finished route split.

     Gives the app its previous size back, removes the split's edge rules
    and deletes the route's app. A split that is still in flight is
    refused with route_split_in_flight; a rolled-back or undone split is
    returned as it is.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RouteSplit]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | RouteSplit | None:
    """Reverse a finished route split.

     Gives the app its previous size back, removes the split's edge rules
    and deletes the route's app. A split that is still in flight is
    refused with route_split_in_flight; a rolled-back or undone split is
    returned as it is.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RouteSplit
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem | RouteSplit]:
    """Reverse a finished route split.

     Gives the app its previous size back, removes the split's edge rules
    and deletes the route's app. A split that is still in flight is
    refused with route_split_in_flight; a rolled-back or undone split is
    returned as it is.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RouteSplit]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | RouteSplit | None:
    """Reverse a finished route split.

     Gives the app its previous size back, removes the split's edge rules
    and deletes the route's app. A split that is still in flight is
    refused with route_split_in_flight; a rolled-back or undone split is
    returned as it is.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RouteSplit
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
        )
    ).parsed
