from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.preview_list import PreviewList
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/previews",
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> PreviewList | Problem | None:
    if response.status_code == 200:
        response_200 = PreviewList.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PreviewList | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[PreviewList | Problem]:
    """List the platform previews you can join.

     Self-serve platform previews (ADR 20261021080640): the previews an
    operator opened to self-serve, plus any you joined that have since
    closed, so you can still leave them. `eligible` says whether your plan
    may join; `enabled` whether the preview is on for you now, for any
    reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PreviewList | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> PreviewList | Problem | None:
    """List the platform previews you can join.

     Self-serve platform previews (ADR 20261021080640): the previews an
    operator opened to self-serve, plus any you joined that have since
    closed, so you can still leave them. `eligible` says whether your plan
    may join; `enabled` whether the preview is on for you now, for any
    reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PreviewList | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[PreviewList | Problem]:
    """List the platform previews you can join.

     Self-serve platform previews (ADR 20261021080640): the previews an
    operator opened to self-serve, plus any you joined that have since
    closed, so you can still leave them. `eligible` says whether your plan
    may join; `enabled` whether the preview is on for you now, for any
    reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PreviewList | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> PreviewList | Problem | None:
    """List the platform previews you can join.

     Self-serve platform previews (ADR 20261021080640): the previews an
    operator opened to self-serve, plus any you joined that have since
    closed, so you can still leave them. `eligible` says whether your plan
    may join; `enabled` whether the preview is on for you now, for any
    reason.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PreviewList | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
