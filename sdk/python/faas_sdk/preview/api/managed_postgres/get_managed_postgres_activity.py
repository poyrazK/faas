from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_activity import ManagedPostgresActivity
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    since: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["since"] = since

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/postgres/databases/{id}/activity".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresActivity | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresActivity.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresActivity | Problem]:
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
    since: str | Unset = UNSET,
) -> Response[ManagedPostgresActivity | Problem]:
    """Show what kept a database awake

     Lists a database's recorded compute starts in the period, newest first (at most 200), each with a
    likely cause, plus each cause's starts and awake time and hints that name the command acting on the
    largest causes. A cause is the first of app_wake (wake-with-app started it), cron (a cron of a bound
    app fired just before), request (a request woke a bound app), app_start (a bound app started without
    a request), running_app (a bound app was running) and external (none of these, so a client outside
    Gregale) that matches the start's timing; it is a best guess, never billing. Starts are recorded
    from the usage sweep, so the latest can lag by up to an hour, and kept 30 days. Reading never wakes
    the database.

    Args:
        id (str):
        since (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresActivity | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        since=since,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
) -> ManagedPostgresActivity | Problem | None:
    """Show what kept a database awake

     Lists a database's recorded compute starts in the period, newest first (at most 200), each with a
    likely cause, plus each cause's starts and awake time and hints that name the command acting on the
    largest causes. A cause is the first of app_wake (wake-with-app started it), cron (a cron of a bound
    app fired just before), request (a request woke a bound app), app_start (a bound app started without
    a request), running_app (a bound app was running) and external (none of these, so a client outside
    Gregale) that matches the start's timing; it is a best guess, never billing. Starts are recorded
    from the usage sweep, so the latest can lag by up to an hour, and kept 30 days. Reading never wakes
    the database.

    Args:
        id (str):
        since (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresActivity | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        since=since,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
) -> Response[ManagedPostgresActivity | Problem]:
    """Show what kept a database awake

     Lists a database's recorded compute starts in the period, newest first (at most 200), each with a
    likely cause, plus each cause's starts and awake time and hints that name the command acting on the
    largest causes. A cause is the first of app_wake (wake-with-app started it), cron (a cron of a bound
    app fired just before), request (a request woke a bound app), app_start (a bound app started without
    a request), running_app (a bound app was running) and external (none of these, so a client outside
    Gregale) that matches the start's timing; it is a best guess, never billing. Starts are recorded
    from the usage sweep, so the latest can lag by up to an hour, and kept 30 days. Reading never wakes
    the database.

    Args:
        id (str):
        since (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresActivity | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        since=since,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    since: str | Unset = UNSET,
) -> ManagedPostgresActivity | Problem | None:
    """Show what kept a database awake

     Lists a database's recorded compute starts in the period, newest first (at most 200), each with a
    likely cause, plus each cause's starts and awake time and hints that name the command acting on the
    largest causes. A cause is the first of app_wake (wake-with-app started it), cron (a cron of a bound
    app fired just before), request (a request woke a bound app), app_start (a bound app started without
    a request), running_app (a bound app was running) and external (none of these, so a client outside
    Gregale) that matches the start's timing; it is a best guess, never billing. Starts are recorded
    from the usage sweep, so the latest can lag by up to an hour, and kept 30 days. Reading never wakes
    the database.

    Args:
        id (str):
        since (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresActivity | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            since=since,
        )
    ).parsed
