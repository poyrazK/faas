from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    after: int | Unset = 0,
    limit: int | Unset = 100,
    last_event_id: int | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(last_event_id, Unset):
        headers["Last-Event-ID"] = str(last_event_id)

    params: dict[str, Any] = {}

    params["after"] = after

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/executions/{id}/events".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | str | None:
    if response.status_code == 200:
        response_200 = response.text
        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Problem | str]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    after: int | Unset = 0,
    limit: int | Unset = 100,
    last_event_id: int | Unset = UNSET,
) -> Response[Problem | str]:
    """Stream disposable execution events.

     Opens a resumable Server-Sent Events stream for one execution. Event
    ids are monotonically increasing control-plane cursors; reconnect with
    `after` or `Last-Event-ID`. The stream emits bounded status, stdout,
    stderr, and terminal events and closes after the terminal event.
    Source, input, host paths, and VM internals are never included.

    Args:
        id (UUID):
        after (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 100.
        last_event_id (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | str]
    """

    kwargs = _get_kwargs(
        id=id,
        after=after,
        limit=limit,
        last_event_id=last_event_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient,
    after: int | Unset = 0,
    limit: int | Unset = 100,
    last_event_id: int | Unset = UNSET,
) -> Problem | str | None:
    """Stream disposable execution events.

     Opens a resumable Server-Sent Events stream for one execution. Event
    ids are monotonically increasing control-plane cursors; reconnect with
    `after` or `Last-Event-ID`. The stream emits bounded status, stdout,
    stderr, and terminal events and closes after the terminal event.
    Source, input, host paths, and VM internals are never included.

    Args:
        id (UUID):
        after (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 100.
        last_event_id (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | str
    """

    return sync_detailed(
        id=id,
        client=client,
        after=after,
        limit=limit,
        last_event_id=last_event_id,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    after: int | Unset = 0,
    limit: int | Unset = 100,
    last_event_id: int | Unset = UNSET,
) -> Response[Problem | str]:
    """Stream disposable execution events.

     Opens a resumable Server-Sent Events stream for one execution. Event
    ids are monotonically increasing control-plane cursors; reconnect with
    `after` or `Last-Event-ID`. The stream emits bounded status, stdout,
    stderr, and terminal events and closes after the terminal event.
    Source, input, host paths, and VM internals are never included.

    Args:
        id (UUID):
        after (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 100.
        last_event_id (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | str]
    """

    kwargs = _get_kwargs(
        id=id,
        after=after,
        limit=limit,
        last_event_id=last_event_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient,
    after: int | Unset = 0,
    limit: int | Unset = 100,
    last_event_id: int | Unset = UNSET,
) -> Problem | str | None:
    """Stream disposable execution events.

     Opens a resumable Server-Sent Events stream for one execution. Event
    ids are monotonically increasing control-plane cursors; reconnect with
    `after` or `Last-Event-ID`. The stream emits bounded status, stdout,
    stderr, and terminal events and closes after the terminal event.
    Source, input, host paths, and VM internals are never included.

    Args:
        id (UUID):
        after (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 100.
        last_event_id (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | str
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            after=after,
            limit=limit,
            last_event_id=last_event_id,
        )
    ).parsed
