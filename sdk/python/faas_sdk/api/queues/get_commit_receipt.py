from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.commit_receipt_response import CommitReceiptResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    source: UUID,
    event: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/commit-sources/{source}/events/{event}".format(
            source=quote(str(source), safe=""),
            event=quote(str(event), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CommitReceiptResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CommitReceiptResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

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
) -> Response[CommitReceiptResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    source: UUID,
    event: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[CommitReceiptResponse | Problem]:
    """Recover acceptance independently of execution completion.

    Args:
        source (UUID):
        event (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CommitReceiptResponse | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        event=event,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    source: UUID,
    event: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> CommitReceiptResponse | Problem | None:
    """Recover acceptance independently of execution completion.

    Args:
        source (UUID):
        event (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CommitReceiptResponse | Problem
    """

    return sync_detailed(
        source=source,
        event=event,
        client=client,
    ).parsed


async def asyncio_detailed(
    source: UUID,
    event: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[CommitReceiptResponse | Problem]:
    """Recover acceptance independently of execution completion.

    Args:
        source (UUID):
        event (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CommitReceiptResponse | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        event=event,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    source: UUID,
    event: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> CommitReceiptResponse | Problem | None:
    """Recover acceptance independently of execution completion.

    Args:
        source (UUID):
        event (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CommitReceiptResponse | Problem
    """

    return (
        await asyncio_detailed(
            source=source,
            event=event,
            client=client,
        )
    ).parsed
