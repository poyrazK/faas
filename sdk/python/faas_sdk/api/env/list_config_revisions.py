from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_config_revisions_response import ListConfigRevisionsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/config/revisions".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListConfigRevisionsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListConfigRevisionsResponse.from_dict(response.json())

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
) -> Response[ListConfigRevisionsResponse | Problem]:
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
    limit: int | Unset = 20,
) -> Response[ListConfigRevisionsResponse | Problem]:
    """List a scope's config revisions (ADR-1451).

     Newest first. Each customer env or secret write records a revision of the scope's complete config;
    the newest 100 are kept. Values are not listed; read one revision for its variables.

    Args:
        slug (str):
        scope (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListConfigRevisionsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        limit=limit,
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
    limit: int | Unset = 20,
) -> ListConfigRevisionsResponse | Problem | None:
    """List a scope's config revisions (ADR-1451).

     Newest first. Each customer env or secret write records a revision of the scope's complete config;
    the newest 100 are kept. Values are not listed; read one revision for its variables.

    Args:
        slug (str):
        scope (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListConfigRevisionsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Response[ListConfigRevisionsResponse | Problem]:
    """List a scope's config revisions (ADR-1451).

     Newest first. Each customer env or secret write records a revision of the scope's complete config;
    the newest 100 are kept. Values are not listed; read one revision for its variables.

    Args:
        slug (str):
        scope (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListConfigRevisionsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> ListConfigRevisionsResponse | Problem | None:
    """List a scope's config revisions (ADR-1451).

     Newest first. Each customer env or secret write records a revision of the scope's complete config;
    the newest 100 are kept. Values are not listed; read one revision for its variables.

    Args:
        slug (str):
        scope (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListConfigRevisionsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            scope=scope,
            limit=limit,
        )
    ).parsed
