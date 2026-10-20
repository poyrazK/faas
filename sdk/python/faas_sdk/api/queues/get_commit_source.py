from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.commit_source_response import CommitSourceResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    source: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/commit-sources/{source}".format(
            source=quote(str(source), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CommitSourceResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CommitSourceResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CommitSourceResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    source: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[CommitSourceResponse | Problem]:
    """Inspect source health and its last observed pending/blocked backlog.

    Args:
        source (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CommitSourceResponse | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    source: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> CommitSourceResponse | Problem | None:
    """Inspect source health and its last observed pending/blocked backlog.

    Args:
        source (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CommitSourceResponse | Problem
    """

    return sync_detailed(
        source=source,
        client=client,
    ).parsed


async def asyncio_detailed(
    source: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[CommitSourceResponse | Problem]:
    """Inspect source health and its last observed pending/blocked backlog.

    Args:
        source (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CommitSourceResponse | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    source: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> CommitSourceResponse | Problem | None:
    """Inspect source health and its last observed pending/blocked backlog.

    Args:
        source (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CommitSourceResponse | Problem
    """

    return (
        await asyncio_detailed(
            source=source,
            client=client,
        )
    ).parsed
