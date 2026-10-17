from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.config_revision_detail import ConfigRevisionDetail
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    revision: int,
    *,
    scope: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/config/revisions/{revision}".format(
            slug=quote(str(slug), safe=""),
            revision=quote(str(revision), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ConfigRevisionDetail | Problem | None:
    if response.status_code == 200:
        response_200 = ConfigRevisionDetail.from_dict(response.json())

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
) -> Response[ConfigRevisionDetail | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    revision: int,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
) -> Response[ConfigRevisionDetail | Problem]:
    """Read one config revision (ADR-1451).

     The scope's variables and secret versions after the change. Secret values and fingerprints are never
    returned.

    Args:
        slug (str):
        revision (int):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConfigRevisionDetail | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        revision=revision,
        scope=scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    revision: int,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
) -> ConfigRevisionDetail | Problem | None:
    """Read one config revision (ADR-1451).

     The scope's variables and secret versions after the change. Secret values and fingerprints are never
    returned.

    Args:
        slug (str):
        revision (int):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConfigRevisionDetail | Problem
    """

    return sync_detailed(
        slug=slug,
        revision=revision,
        client=client,
        scope=scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    revision: int,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
) -> Response[ConfigRevisionDetail | Problem]:
    """Read one config revision (ADR-1451).

     The scope's variables and secret versions after the change. Secret values and fingerprints are never
    returned.

    Args:
        slug (str):
        revision (int):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConfigRevisionDetail | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        revision=revision,
        scope=scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    revision: int,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
) -> ConfigRevisionDetail | Problem | None:
    """Read one config revision (ADR-1451).

     The scope's variables and secret versions after the change. Secret values and fingerprints are never
    returned.

    Args:
        slug (str):
        revision (int):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConfigRevisionDetail | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            revision=revision,
            client=client,
            scope=scope,
        )
    ).parsed
