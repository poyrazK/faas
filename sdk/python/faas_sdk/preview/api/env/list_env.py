from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_env_list_response import AppEnvListResponse
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
        "url": "/v1/apps/{slug}/env".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppEnvListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppEnvListResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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
) -> Response[AppEnvListResponse | Problem]:
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
) -> Response[AppEnvListResponse | Problem]:
    """List env vars on an app.

     Returns every env var key + timestamps on the app. The plaintext
    value NEVER appears in the response — guest-init reads the value
    at process start from `/etc/faas/env.json` inside the guest.

    **ADR-090 PR-B scope filter.** The optional `?scope=`
    query param selects which scope to read. Omitted = the
    default scope (pre-PR-B behavior, byte-identical wire).
    `?scope=__all__` returns the nested `env_by_scope` response
    shape with every scope on the app; the flat `env` array
    is empty in that arm (discriminated union). Any other
    `?scope=<slug>` filters to that one scope. Invalid scope
    values return 400 `env_scope_invalid`.

    Args:
        slug (str):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppEnvListResponse | Problem]
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
) -> AppEnvListResponse | Problem | None:
    """List env vars on an app.

     Returns every env var key + timestamps on the app. The plaintext
    value NEVER appears in the response — guest-init reads the value
    at process start from `/etc/faas/env.json` inside the guest.

    **ADR-090 PR-B scope filter.** The optional `?scope=`
    query param selects which scope to read. Omitted = the
    default scope (pre-PR-B behavior, byte-identical wire).
    `?scope=__all__` returns the nested `env_by_scope` response
    shape with every scope on the app; the flat `env` array
    is empty in that arm (discriminated union). Any other
    `?scope=<slug>` filters to that one scope. Invalid scope
    values return 400 `env_scope_invalid`.

    Args:
        slug (str):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppEnvListResponse | Problem
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
) -> Response[AppEnvListResponse | Problem]:
    """List env vars on an app.

     Returns every env var key + timestamps on the app. The plaintext
    value NEVER appears in the response — guest-init reads the value
    at process start from `/etc/faas/env.json` inside the guest.

    **ADR-090 PR-B scope filter.** The optional `?scope=`
    query param selects which scope to read. Omitted = the
    default scope (pre-PR-B behavior, byte-identical wire).
    `?scope=__all__` returns the nested `env_by_scope` response
    shape with every scope on the app; the flat `env` array
    is empty in that arm (discriminated union). Any other
    `?scope=<slug>` filters to that one scope. Invalid scope
    values return 400 `env_scope_invalid`.

    Args:
        slug (str):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppEnvListResponse | Problem]
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
) -> AppEnvListResponse | Problem | None:
    """List env vars on an app.

     Returns every env var key + timestamps on the app. The plaintext
    value NEVER appears in the response — guest-init reads the value
    at process start from `/etc/faas/env.json` inside the guest.

    **ADR-090 PR-B scope filter.** The optional `?scope=`
    query param selects which scope to read. Omitted = the
    default scope (pre-PR-B behavior, byte-identical wire).
    `?scope=__all__` returns the nested `env_by_scope` response
    shape with every scope on the app; the flat `env` array
    is empty in that arm (discriminated union). Any other
    `?scope=<slug>` filters to that one scope. Invalid scope
    values return 400 `env_scope_invalid`.

    Args:
        slug (str):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppEnvListResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            scope=scope,
        )
    ).parsed
