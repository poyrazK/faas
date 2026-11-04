from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.config_guard import ConfigGuard
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/config/guard".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ConfigGuard | Problem | None:
    if response.status_code == 200:
        response_200 = ConfigGuard.from_dict(response.json())

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
) -> Response[ConfigGuard | Problem]:
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
    scope: str | Unset = UNSET,
) -> Response[ConfigGuard | Problem]:
    """Read a scope's newest config guard.

     A guard judges the first traffic served with a changed config against the config before it, and
    restores the previous config when the new one fails. 404 `config_guard_not_found` when no change in
    the scope was guarded.

    Args:
        slug (str):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConfigGuard | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
) -> ConfigGuard | Problem | None:
    """Read a scope's newest config guard.

     A guard judges the first traffic served with a changed config against the config before it, and
    restores the previous config when the new one fails. 404 `config_guard_not_found` when no change in
    the scope was guarded.

    Args:
        slug (str):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConfigGuard | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
) -> Response[ConfigGuard | Problem]:
    """Read a scope's newest config guard.

     A guard judges the first traffic served with a changed config against the config before it, and
    restores the previous config when the new one fails. 404 `config_guard_not_found` when no change in
    the scope was guarded.

    Args:
        slug (str):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConfigGuard | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
) -> ConfigGuard | Problem | None:
    """Read a scope's newest config guard.

     A guard judges the first traffic served with a changed config against the config before it, and
    restores the previous config when the new one fails. 404 `config_guard_not_found` when no change in
    the scope was guarded.

    Args:
        slug (str):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConfigGuard | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            scope=scope,
        )
    ).parsed
