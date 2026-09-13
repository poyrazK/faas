from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_admin_status_events_kind import ListAdminStatusEventsKind
from ...models.problem import Problem
from ...models.public_status_event import PublicStatusEvent
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    kind: ListAdminStatusEventsKind | Unset = UNSET,
    active: bool | Unset = False,
    faas_sid: str | Unset = UNSET,
) -> dict[str, Any]:

    cookies = {}
    if faas_sid is not UNSET:
        cookies["faas_sid"] = faas_sid

    params: dict[str, Any] = {}

    json_kind: str | Unset = UNSET
    if not isinstance(kind, Unset):
        json_kind = kind

    params["kind"] = json_kind

    params["active"] = active

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/status/incidents",
        "params": params,
        "cookies": cookies,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | list[PublicStatusEvent] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = PublicStatusEvent.from_dict(response_200_item_data)

            response_200.append(response_200_item)

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | list[PublicStatusEvent]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    kind: ListAdminStatusEventsKind | Unset = UNSET,
    active: bool | Unset = False,
    faas_sid: str | Unset = UNSET,
) -> Response[Problem | list[PublicStatusEvent]]:
    """List public status events (admin-only).

     Requires admin scope, operator allowlist membership, and MFA.

    Args:
        kind (ListAdminStatusEventsKind | Unset):
        active (bool | Unset):  Default: False.
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[PublicStatusEvent]]
    """

    kwargs = _get_kwargs(
        kind=kind,
        active=active,
        faas_sid=faas_sid,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    kind: ListAdminStatusEventsKind | Unset = UNSET,
    active: bool | Unset = False,
    faas_sid: str | Unset = UNSET,
) -> Problem | list[PublicStatusEvent] | None:
    """List public status events (admin-only).

     Requires admin scope, operator allowlist membership, and MFA.

    Args:
        kind (ListAdminStatusEventsKind | Unset):
        active (bool | Unset):  Default: False.
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[PublicStatusEvent]
    """

    return sync_detailed(
        client=client,
        kind=kind,
        active=active,
        faas_sid=faas_sid,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    kind: ListAdminStatusEventsKind | Unset = UNSET,
    active: bool | Unset = False,
    faas_sid: str | Unset = UNSET,
) -> Response[Problem | list[PublicStatusEvent]]:
    """List public status events (admin-only).

     Requires admin scope, operator allowlist membership, and MFA.

    Args:
        kind (ListAdminStatusEventsKind | Unset):
        active (bool | Unset):  Default: False.
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[PublicStatusEvent]]
    """

    kwargs = _get_kwargs(
        kind=kind,
        active=active,
        faas_sid=faas_sid,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    kind: ListAdminStatusEventsKind | Unset = UNSET,
    active: bool | Unset = False,
    faas_sid: str | Unset = UNSET,
) -> Problem | list[PublicStatusEvent] | None:
    """List public status events (admin-only).

     Requires admin scope, operator allowlist membership, and MFA.

    Args:
        kind (ListAdminStatusEventsKind | Unset):
        active (bool | Unset):  Default: False.
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[PublicStatusEvent]
    """

    return (
        await asyncio_detailed(
            client=client,
            kind=kind,
            active=active,
            faas_sid=faas_sid,
        )
    ).parsed
