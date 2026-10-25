from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.incident_note import IncidentNote
from ...models.incident_note_request import IncidentNoteRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: IncidentNoteRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/incidents/{id}/notes".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> IncidentNote | Problem:
    if response.status_code == 201:
        response_201 = IncidentNote.from_dict(response.json())

        return response_201

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[IncidentNote | Problem]:
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
    body: IncidentNoteRequest,
) -> Response[IncidentNote | Problem]:
    """Add a note to an incident.

     Appends a note to an open or resolved incident. Notes cannot be
    edited or deleted. A note is at most 2000 characters, and an
    incident holds at most 100 notes from an account, its
    acknowledgement included.

    Args:
        id (str):
        body (IncidentNoteRequest): A note to append to an incident.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IncidentNote | Problem]
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
    body: IncidentNoteRequest,
) -> IncidentNote | Problem | None:
    """Add a note to an incident.

     Appends a note to an open or resolved incident. Notes cannot be
    edited or deleted. A note is at most 2000 characters, and an
    incident holds at most 100 notes from an account, its
    acknowledgement included.

    Args:
        id (str):
        body (IncidentNoteRequest): A note to append to an incident.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IncidentNote | Problem
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
    body: IncidentNoteRequest,
) -> Response[IncidentNote | Problem]:
    """Add a note to an incident.

     Appends a note to an open or resolved incident. Notes cannot be
    edited or deleted. A note is at most 2000 characters, and an
    incident holds at most 100 notes from an account, its
    acknowledgement included.

    Args:
        id (str):
        body (IncidentNoteRequest): A note to append to an incident.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IncidentNote | Problem]
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
    body: IncidentNoteRequest,
) -> IncidentNote | Problem | None:
    """Add a note to an incident.

     Appends a note to an open or resolved incident. Notes cannot be
    edited or deleted. A note is at most 2000 characters, and an
    incident holds at most 100 notes from an account, its
    acknowledgement included.

    Args:
        id (str):
        body (IncidentNoteRequest): A note to append to an incident.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IncidentNote | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
