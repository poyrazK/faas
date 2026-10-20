import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_recovery_jobs import EventRecoveryJobs
from ...models.list_event_recoveries_mode import ListEventRecoveriesMode
from ...models.list_event_recoveries_state import ListEventRecoveriesState
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    state: ListEventRecoveriesState | Unset = UNSET,
    mode: ListEventRecoveriesMode | Unset = UNSET,
    subscription_id: str | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_state: str | Unset = UNSET
    if not isinstance(state, Unset):
        json_state = state

    params["state"] = json_state

    json_mode: str | Unset = UNSET
    if not isinstance(mode, Unset):
        json_mode = mode

    params["mode"] = json_mode

    params["subscription_id"] = subscription_id

    json_created_after: str | Unset = UNSET
    if not isinstance(created_after, Unset):
        json_created_after = created_after.isoformat()
    params["created_after"] = json_created_after

    json_created_before: str | Unset = UNSET
    if not isinstance(created_before, Unset):
        json_created_before = created_before.isoformat()
    params["created_before"] = json_created_before

    params["cursor"] = cursor

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-recoveries".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventRecoveryJobs | Problem | None:
    if response.status_code == 200:
        response_200 = EventRecoveryJobs.from_dict(response.json())

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

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventRecoveryJobs | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    state: ListEventRecoveriesState | Unset = UNSET,
    mode: ListEventRecoveriesMode | Unset = UNSET,
    subscription_id: str | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[EventRecoveryJobs | Problem]:
    """Discover retained recovery jobs for an owned application.

     Requires apps:read or admin and MFA. Newest creation time and ID first. Lists admission metadata
    only; execution outcomes are available through recovery status. Pages reflect live state and
    retention, not a frozen snapshot. Reuse the same app and filters with next_cursor.

    Args:
        slug (str):
        state (ListEventRecoveriesState | Unset):
        mode (ListEventRecoveriesMode | Unset):
        subscription_id (str | Unset):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):
        cursor (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryJobs | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        state=state,
        mode=mode,
        subscription_id=subscription_id,
        created_after=created_after,
        created_before=created_before,
        cursor=cursor,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    state: ListEventRecoveriesState | Unset = UNSET,
    mode: ListEventRecoveriesMode | Unset = UNSET,
    subscription_id: str | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> EventRecoveryJobs | Problem | None:
    """Discover retained recovery jobs for an owned application.

     Requires apps:read or admin and MFA. Newest creation time and ID first. Lists admission metadata
    only; execution outcomes are available through recovery status. Pages reflect live state and
    retention, not a frozen snapshot. Reuse the same app and filters with next_cursor.

    Args:
        slug (str):
        state (ListEventRecoveriesState | Unset):
        mode (ListEventRecoveriesMode | Unset):
        subscription_id (str | Unset):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):
        cursor (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryJobs | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        state=state,
        mode=mode,
        subscription_id=subscription_id,
        created_after=created_after,
        created_before=created_before,
        cursor=cursor,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    state: ListEventRecoveriesState | Unset = UNSET,
    mode: ListEventRecoveriesMode | Unset = UNSET,
    subscription_id: str | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[EventRecoveryJobs | Problem]:
    """Discover retained recovery jobs for an owned application.

     Requires apps:read or admin and MFA. Newest creation time and ID first. Lists admission metadata
    only; execution outcomes are available through recovery status. Pages reflect live state and
    retention, not a frozen snapshot. Reuse the same app and filters with next_cursor.

    Args:
        slug (str):
        state (ListEventRecoveriesState | Unset):
        mode (ListEventRecoveriesMode | Unset):
        subscription_id (str | Unset):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):
        cursor (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryJobs | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        state=state,
        mode=mode,
        subscription_id=subscription_id,
        created_after=created_after,
        created_before=created_before,
        cursor=cursor,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    state: ListEventRecoveriesState | Unset = UNSET,
    mode: ListEventRecoveriesMode | Unset = UNSET,
    subscription_id: str | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> EventRecoveryJobs | Problem | None:
    """Discover retained recovery jobs for an owned application.

     Requires apps:read or admin and MFA. Newest creation time and ID first. Lists admission metadata
    only; execution outcomes are available through recovery status. Pages reflect live state and
    retention, not a frozen snapshot. Reuse the same app and filters with next_cursor.

    Args:
        slug (str):
        state (ListEventRecoveriesState | Unset):
        mode (ListEventRecoveriesMode | Unset):
        subscription_id (str | Unset):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):
        cursor (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryJobs | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            state=state,
            mode=mode,
            subscription_id=subscription_id,
            created_after=created_after,
            created_before=created_before,
            cursor=cursor,
            limit=limit,
        )
    ).parsed
