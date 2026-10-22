from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.incident_detail import IncidentDetail
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/incidents/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> IncidentDetail | Problem:
    if response.status_code == 200:
        response_200 = IncidentDetail.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[IncidentDetail | Problem]:
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
) -> Response[IncidentDetail | Problem]:
    """Read an incident and its evidence.

     The incident with what the platform has for it: an alert's
    notifications and the log context its newest one carried, a platform
    incident's status updates, and for an app incident the app's change
    timeline from an hour before it opened until it resolved.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IncidentDetail | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> IncidentDetail | Problem | None:
    """Read an incident and its evidence.

     The incident with what the platform has for it: an alert's
    notifications and the log context its newest one carried, a platform
    incident's status updates, and for an app incident the app's change
    timeline from an hour before it opened until it resolved.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IncidentDetail | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[IncidentDetail | Problem]:
    """Read an incident and its evidence.

     The incident with what the platform has for it: an alert's
    notifications and the log context its newest one carried, a platform
    incident's status updates, and for an app incident the app's change
    timeline from an hour before it opened until it resolved.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IncidentDetail | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> IncidentDetail | Problem | None:
    """Read an incident and its evidence.

     The incident with what the platform has for it: an alert's
    notifications and the log context its newest one carried, a platform
    incident's status updates, and for an app incident the app's change
    timeline from an hour before it opened until it resolved.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IncidentDetail | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
