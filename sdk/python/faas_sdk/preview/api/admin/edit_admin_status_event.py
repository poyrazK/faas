from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.admin_status_event_edit_request import AdminStatusEventEditRequest
from ...models.problem import Problem
from ...models.public_status_event import PublicStatusEvent
from ...types import UNSET, Response, Unset


def _get_kwargs(
    public_id: UUID,
    *,
    body: AdminStatusEventEditRequest,
    faas_sid: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    cookies = {}
    if faas_sid is not UNSET:
        cookies["faas_sid"] = faas_sid

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/admin/status/incidents/{public_id}".format(
            public_id=quote(str(public_id), safe=""),
        ),
        "cookies": cookies,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | PublicStatusEvent | None:
    if response.status_code == 200:
        response_200 = PublicStatusEvent.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | PublicStatusEvent]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    public_id: UUID,
    *,
    client: AuthenticatedClient,
    body: AdminStatusEventEditRequest,
    faas_sid: str | Unset = UNSET,
) -> Response[Problem | PublicStatusEvent]:
    """Correct a published event title without changing its timeline order.

     Requires an operator session. The event remains published and exposes edited_at; deletion is
    unsupported.

    Args:
        public_id (UUID):
        faas_sid (str | Unset):
        body (AdminStatusEventEditRequest): Operator correction to a published event title.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | PublicStatusEvent]
    """

    kwargs = _get_kwargs(
        public_id=public_id,
        body=body,
        faas_sid=faas_sid,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    public_id: UUID,
    *,
    client: AuthenticatedClient,
    body: AdminStatusEventEditRequest,
    faas_sid: str | Unset = UNSET,
) -> Problem | PublicStatusEvent | None:
    """Correct a published event title without changing its timeline order.

     Requires an operator session. The event remains published and exposes edited_at; deletion is
    unsupported.

    Args:
        public_id (UUID):
        faas_sid (str | Unset):
        body (AdminStatusEventEditRequest): Operator correction to a published event title.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | PublicStatusEvent
    """

    return sync_detailed(
        public_id=public_id,
        client=client,
        body=body,
        faas_sid=faas_sid,
    ).parsed


async def asyncio_detailed(
    public_id: UUID,
    *,
    client: AuthenticatedClient,
    body: AdminStatusEventEditRequest,
    faas_sid: str | Unset = UNSET,
) -> Response[Problem | PublicStatusEvent]:
    """Correct a published event title without changing its timeline order.

     Requires an operator session. The event remains published and exposes edited_at; deletion is
    unsupported.

    Args:
        public_id (UUID):
        faas_sid (str | Unset):
        body (AdminStatusEventEditRequest): Operator correction to a published event title.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | PublicStatusEvent]
    """

    kwargs = _get_kwargs(
        public_id=public_id,
        body=body,
        faas_sid=faas_sid,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    public_id: UUID,
    *,
    client: AuthenticatedClient,
    body: AdminStatusEventEditRequest,
    faas_sid: str | Unset = UNSET,
) -> Problem | PublicStatusEvent | None:
    """Correct a published event title without changing its timeline order.

     Requires an operator session. The event remains published and exposes edited_at; deletion is
    unsupported.

    Args:
        public_id (UUID):
        faas_sid (str | Unset):
        body (AdminStatusEventEditRequest): Operator correction to a published event title.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | PublicStatusEvent
    """

    return (
        await asyncio_detailed(
            public_id=public_id,
            client=client,
            body=body,
            faas_sid=faas_sid,
        )
    ).parsed
