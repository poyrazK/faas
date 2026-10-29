from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_recovery_notification_retry_backlog import EventRecoveryNotificationRetryBacklog
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    status: str | Unset = "failed,pending,inconclusive",
    page_size: int | Unset = 5,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["status"] = status

    params["page_size"] = page_size

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-recoveries/notification-retry-backlog".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventRecoveryNotificationRetryBacklog | Problem | None:
    if response.status_code == 200:
        response_200 = EventRecoveryNotificationRetryBacklog.from_dict(response.json())

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

    if response.status_code == 500:
        response_500 = Problem.from_dict(response.json())

        return response_500

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventRecoveryNotificationRetryBacklog | Problem]:
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
    status: str | Unset = "failed,pending,inconclusive",
    page_size: int | Unset = 5,
    cursor: str | Unset = UNSET,
) -> Response[EventRecoveryNotificationRetryBacklog | Problem]:
    """Inspect app-wide recovery notification retry backlog.

     Requires apps:read or admin and MFA. Reads original-generation retry outcomes across a bounded page
    of owned retained recovery jobs containing saved retry requests. Defaults to failed, pending, and
    inconclusive requests. Totals cover every request in scanned jobs before status filtering and are
    explicitly job-page scoped. Empty filtered pages may still have a next cursor. Each page is a fresh
    read-only snapshot; refresh from the beginning to see newer jobs or updated previously scanned jobs.

    Args:
        slug (str):
        status (str | Unset):  Default: 'failed,pending,inconclusive'.
        page_size (int | Unset):  Default: 5.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryNotificationRetryBacklog | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        status=status,
        page_size=page_size,
        cursor=cursor,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    status: str | Unset = "failed,pending,inconclusive",
    page_size: int | Unset = 5,
    cursor: str | Unset = UNSET,
) -> EventRecoveryNotificationRetryBacklog | Problem | None:
    """Inspect app-wide recovery notification retry backlog.

     Requires apps:read or admin and MFA. Reads original-generation retry outcomes across a bounded page
    of owned retained recovery jobs containing saved retry requests. Defaults to failed, pending, and
    inconclusive requests. Totals cover every request in scanned jobs before status filtering and are
    explicitly job-page scoped. Empty filtered pages may still have a next cursor. Each page is a fresh
    read-only snapshot; refresh from the beginning to see newer jobs or updated previously scanned jobs.

    Args:
        slug (str):
        status (str | Unset):  Default: 'failed,pending,inconclusive'.
        page_size (int | Unset):  Default: 5.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryNotificationRetryBacklog | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        status=status,
        page_size=page_size,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    status: str | Unset = "failed,pending,inconclusive",
    page_size: int | Unset = 5,
    cursor: str | Unset = UNSET,
) -> Response[EventRecoveryNotificationRetryBacklog | Problem]:
    """Inspect app-wide recovery notification retry backlog.

     Requires apps:read or admin and MFA. Reads original-generation retry outcomes across a bounded page
    of owned retained recovery jobs containing saved retry requests. Defaults to failed, pending, and
    inconclusive requests. Totals cover every request in scanned jobs before status filtering and are
    explicitly job-page scoped. Empty filtered pages may still have a next cursor. Each page is a fresh
    read-only snapshot; refresh from the beginning to see newer jobs or updated previously scanned jobs.

    Args:
        slug (str):
        status (str | Unset):  Default: 'failed,pending,inconclusive'.
        page_size (int | Unset):  Default: 5.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryNotificationRetryBacklog | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        status=status,
        page_size=page_size,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    status: str | Unset = "failed,pending,inconclusive",
    page_size: int | Unset = 5,
    cursor: str | Unset = UNSET,
) -> EventRecoveryNotificationRetryBacklog | Problem | None:
    """Inspect app-wide recovery notification retry backlog.

     Requires apps:read or admin and MFA. Reads original-generation retry outcomes across a bounded page
    of owned retained recovery jobs containing saved retry requests. Defaults to failed, pending, and
    inconclusive requests. Totals cover every request in scanned jobs before status filtering and are
    explicitly job-page scoped. Empty filtered pages may still have a next cursor. Each page is a fresh
    read-only snapshot; refresh from the beginning to see newer jobs or updated previously scanned jobs.

    Args:
        slug (str):
        status (str | Unset):  Default: 'failed,pending,inconclusive'.
        page_size (int | Unset):  Default: 5.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryNotificationRetryBacklog | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            status=status,
            page_size=page_size,
            cursor=cursor,
        )
    ).parsed
