from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_suspend_timeout import ManagedPostgresSuspendTimeout
from ...models.problem import Problem
from ...models.set_managed_postgres_suspend_timeout_request import SetManagedPostgresSuspendTimeoutRequest
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: SetManagedPostgresSuspendTimeoutRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/postgres/databases/{id}/suspend-timeout".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresSuspendTimeout | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresSuspendTimeout.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresSuspendTimeout | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresSuspendTimeoutRequest,
) -> Response[ManagedPostgresSuspendTimeout | Problem]:
    """Set when a scale-to-zero database suspends

     Sets how long a scale-to-zero database stays up after its last activity, from 60 to 3600 seconds, or
    with null returns it to the platform default. Every wake bills at least this much compute. A
    suspended database takes the value at once; a running one takes it the next time it suspends,
    without interrupting connections. Repeating the same value is harmless. seconds is required; null is
    the default. An always-on database answers 409 suspend_timeout_always_on; one with a change in
    progress answers a conflict. While operations are closed, only a shorter timeout is taken.

    Args:
        id (str):
        body (SetManagedPostgresSuspendTimeoutRequest): How long a scale-to-zero database stays up
            after its last activity before it suspends.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresSuspendTimeout | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresSuspendTimeoutRequest,
) -> ManagedPostgresSuspendTimeout | Problem | None:
    """Set when a scale-to-zero database suspends

     Sets how long a scale-to-zero database stays up after its last activity, from 60 to 3600 seconds, or
    with null returns it to the platform default. Every wake bills at least this much compute. A
    suspended database takes the value at once; a running one takes it the next time it suspends,
    without interrupting connections. Repeating the same value is harmless. seconds is required; null is
    the default. An always-on database answers 409 suspend_timeout_always_on; one with a change in
    progress answers a conflict. While operations are closed, only a shorter timeout is taken.

    Args:
        id (str):
        body (SetManagedPostgresSuspendTimeoutRequest): How long a scale-to-zero database stays up
            after its last activity before it suspends.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresSuspendTimeout | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresSuspendTimeoutRequest,
) -> Response[ManagedPostgresSuspendTimeout | Problem]:
    """Set when a scale-to-zero database suspends

     Sets how long a scale-to-zero database stays up after its last activity, from 60 to 3600 seconds, or
    with null returns it to the platform default. Every wake bills at least this much compute. A
    suspended database takes the value at once; a running one takes it the next time it suspends,
    without interrupting connections. Repeating the same value is harmless. seconds is required; null is
    the default. An always-on database answers 409 suspend_timeout_always_on; one with a change in
    progress answers a conflict. While operations are closed, only a shorter timeout is taken.

    Args:
        id (str):
        body (SetManagedPostgresSuspendTimeoutRequest): How long a scale-to-zero database stays up
            after its last activity before it suspends.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresSuspendTimeout | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresSuspendTimeoutRequest,
) -> ManagedPostgresSuspendTimeout | Problem | None:
    """Set when a scale-to-zero database suspends

     Sets how long a scale-to-zero database stays up after its last activity, from 60 to 3600 seconds, or
    with null returns it to the platform default. Every wake bills at least this much compute. A
    suspended database takes the value at once; a running one takes it the next time it suspends,
    without interrupting connections. Repeating the same value is harmless. seconds is required; null is
    the default. An always-on database answers 409 suspend_timeout_always_on; one with a change in
    progress answers a conflict. While operations are closed, only a shorter timeout is taken.

    Args:
        id (str):
        body (SetManagedPostgresSuspendTimeoutRequest): How long a scale-to-zero database stays up
            after its last activity before it suspends.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresSuspendTimeout | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
