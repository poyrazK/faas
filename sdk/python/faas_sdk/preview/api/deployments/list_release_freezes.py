from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_release_freezes_response import ListReleaseFreezesResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["app"] = app

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/freezes",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListReleaseFreezesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListReleaseFreezesResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ListReleaseFreezesResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
) -> Response[ListReleaseFreezesResponse | Problem]:
    """List deploy freezes.

     Unlifted freezes that have not ended: active and upcoming windows, weekly windows and after-rollback
    policies. `app` narrows the list to the freezes covering one app.

    Args:
        app (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListReleaseFreezesResponse | Problem]
    """

    kwargs = _get_kwargs(
        app=app,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
) -> ListReleaseFreezesResponse | Problem | None:
    """List deploy freezes.

     Unlifted freezes that have not ended: active and upcoming windows, weekly windows and after-rollback
    policies. `app` narrows the list to the freezes covering one app.

    Args:
        app (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListReleaseFreezesResponse | Problem
    """

    return sync_detailed(
        client=client,
        app=app,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
) -> Response[ListReleaseFreezesResponse | Problem]:
    """List deploy freezes.

     Unlifted freezes that have not ended: active and upcoming windows, weekly windows and after-rollback
    policies. `app` narrows the list to the freezes covering one app.

    Args:
        app (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListReleaseFreezesResponse | Problem]
    """

    kwargs = _get_kwargs(
        app=app,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
) -> ListReleaseFreezesResponse | Problem | None:
    """List deploy freezes.

     Unlifted freezes that have not ended: active and upcoming windows, weekly windows and after-rollback
    policies. `app` narrows the list to the freezes covering one app.

    Args:
        app (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListReleaseFreezesResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            app=app,
        )
    ).parsed
