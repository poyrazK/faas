from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.env_diff_response import EnvDiffResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/env-diff".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EnvDiffResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EnvDiffResponse.from_dict(response.json())

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
) -> Response[EnvDiffResponse | Problem]:
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
) -> Response[EnvDiffResponse | Problem]:
    """Render the env-diff matrix (presence / value-equality across scopes).

     Returns the (rows × scopes) matrix of env vars + sealed
    secrets on the app. Secrets never reveal plaintext — the
    cell carries {present, value_hash} for secret rows and
    {present, value} for env rows. Two cells with the same
    `value_hash` therefore share byte-identical plaintext
    (collision probability 2^-64). Pre-PR-C rows have
    `value_hash = ''` and emit no `value_hash` key.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvDiffResponse | Problem]
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
) -> EnvDiffResponse | Problem | None:
    """Render the env-diff matrix (presence / value-equality across scopes).

     Returns the (rows × scopes) matrix of env vars + sealed
    secrets on the app. Secrets never reveal plaintext — the
    cell carries {present, value_hash} for secret rows and
    {present, value} for env rows. Two cells with the same
    `value_hash` therefore share byte-identical plaintext
    (collision probability 2^-64). Pre-PR-C rows have
    `value_hash = ''` and emit no `value_hash` key.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvDiffResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[EnvDiffResponse | Problem]:
    """Render the env-diff matrix (presence / value-equality across scopes).

     Returns the (rows × scopes) matrix of env vars + sealed
    secrets on the app. Secrets never reveal plaintext — the
    cell carries {present, value_hash} for secret rows and
    {present, value} for env rows. Two cells with the same
    `value_hash` therefore share byte-identical plaintext
    (collision probability 2^-64). Pre-PR-C rows have
    `value_hash = ''` and emit no `value_hash` key.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvDiffResponse | Problem]
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
) -> EnvDiffResponse | Problem | None:
    """Render the env-diff matrix (presence / value-equality across scopes).

     Returns the (rows × scopes) matrix of env vars + sealed
    secrets on the app. Secrets never reveal plaintext — the
    cell carries {present, value_hash} for secret rows and
    {present, value} for env rows. Two cells with the same
    `value_hash` therefore share byte-identical plaintext
    (collision probability 2^-64). Pre-PR-C rows have
    `value_hash = ''` and emit no `value_hash` key.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvDiffResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
