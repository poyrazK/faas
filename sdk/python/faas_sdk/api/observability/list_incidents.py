import datetime
from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.incidents_response import IncidentsResponse
from ...models.list_incidents_status import ListIncidentsStatus
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app: str | Unset = UNSET,
    status: ListIncidentsStatus | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["app"] = app

    json_status: str | Unset = UNSET
    if not isinstance(status, Unset):
        json_status = status

    params["status"] = json_status

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/incidents",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> IncidentsResponse | Problem:
    if response.status_code == 200:
        response_200 = IncidentsResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[IncidentsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
    status: ListIncidentsStatus | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
) -> Response[IncidentsResponse | Problem]:
    """List the account's incidents.

     ADR-20261022193512 internal preview behind `FAAS_INCIDENTS_ENABLED=1`.
    One list of alert rule firing periods, route monitor incidents and
    platform incidents from the status page: everything open plus what
    resolved since `since`, open first, then newest first, at most 100.
    Each entry says who in the account acknowledged it. A source that
    cannot be read (including `acknowledgements`) is named in
    `unavailable_sources`.

    Args:
        app (str | Unset):
        status (ListIncidentsStatus | Unset):
        since (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IncidentsResponse | Problem]
    """

    kwargs = _get_kwargs(
        app=app,
        status=status,
        since=since,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
    status: ListIncidentsStatus | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
) -> IncidentsResponse | Problem | None:
    """List the account's incidents.

     ADR-20261022193512 internal preview behind `FAAS_INCIDENTS_ENABLED=1`.
    One list of alert rule firing periods, route monitor incidents and
    platform incidents from the status page: everything open plus what
    resolved since `since`, open first, then newest first, at most 100.
    Each entry says who in the account acknowledged it. A source that
    cannot be read (including `acknowledgements`) is named in
    `unavailable_sources`.

    Args:
        app (str | Unset):
        status (ListIncidentsStatus | Unset):
        since (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IncidentsResponse | Problem
    """

    return sync_detailed(
        client=client,
        app=app,
        status=status,
        since=since,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
    status: ListIncidentsStatus | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
) -> Response[IncidentsResponse | Problem]:
    """List the account's incidents.

     ADR-20261022193512 internal preview behind `FAAS_INCIDENTS_ENABLED=1`.
    One list of alert rule firing periods, route monitor incidents and
    platform incidents from the status page: everything open plus what
    resolved since `since`, open first, then newest first, at most 100.
    Each entry says who in the account acknowledged it. A source that
    cannot be read (including `acknowledgements`) is named in
    `unavailable_sources`.

    Args:
        app (str | Unset):
        status (ListIncidentsStatus | Unset):
        since (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IncidentsResponse | Problem]
    """

    kwargs = _get_kwargs(
        app=app,
        status=status,
        since=since,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    app: str | Unset = UNSET,
    status: ListIncidentsStatus | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
) -> IncidentsResponse | Problem | None:
    """List the account's incidents.

     ADR-20261022193512 internal preview behind `FAAS_INCIDENTS_ENABLED=1`.
    One list of alert rule firing periods, route monitor incidents and
    platform incidents from the status page: everything open plus what
    resolved since `since`, open first, then newest first, at most 100.
    Each entry says who in the account acknowledged it. A source that
    cannot be read (including `acknowledgements`) is named in
    `unavailable_sources`.

    Args:
        app (str | Unset):
        status (ListIncidentsStatus | Unset):
        since (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IncidentsResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            app=app,
            status=status,
            since=since,
        )
    ).parsed
