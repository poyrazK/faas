from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.support_notification_page import SupportNotificationPage
from ...types import UNSET, Response, Unset


def _get_kwargs(
    org: str,
    *,
    before: int | Unset = UNSET,
    unread: bool | Unset = False,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["before"] = before

    params["unread"] = unread

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{org}/support/notifications".format(
            org=quote(str(org), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SupportNotificationPage | None:
    if response.status_code == 200:
        response_200 = SupportNotificationPage.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | SupportNotificationPage]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    org: str,
    *,
    client: AuthenticatedClient,
    before: int | Unset = UNSET,
    unread: bool | Unset = False,
) -> Response[Problem | SupportNotificationPage]:
    """List organization support notifications

     Organization support. Metadata-only updates for the current caller. Read state is personal to this
    account and support audience. Private notes never create customer notifications.

    Args:
        org (str):
        before (int | Unset):
        unread (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SupportNotificationPage]
    """

    kwargs = _get_kwargs(
        org=org,
        before=before,
        unread=unread,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    org: str,
    *,
    client: AuthenticatedClient,
    before: int | Unset = UNSET,
    unread: bool | Unset = False,
) -> Problem | SupportNotificationPage | None:
    """List organization support notifications

     Organization support. Metadata-only updates for the current caller. Read state is personal to this
    account and support audience. Private notes never create customer notifications.

    Args:
        org (str):
        before (int | Unset):
        unread (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SupportNotificationPage
    """

    return sync_detailed(
        org=org,
        client=client,
        before=before,
        unread=unread,
    ).parsed


async def asyncio_detailed(
    org: str,
    *,
    client: AuthenticatedClient,
    before: int | Unset = UNSET,
    unread: bool | Unset = False,
) -> Response[Problem | SupportNotificationPage]:
    """List organization support notifications

     Organization support. Metadata-only updates for the current caller. Read state is personal to this
    account and support audience. Private notes never create customer notifications.

    Args:
        org (str):
        before (int | Unset):
        unread (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SupportNotificationPage]
    """

    kwargs = _get_kwargs(
        org=org,
        before=before,
        unread=unread,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    org: str,
    *,
    client: AuthenticatedClient,
    before: int | Unset = UNSET,
    unread: bool | Unset = False,
) -> Problem | SupportNotificationPage | None:
    """List organization support notifications

     Organization support. Metadata-only updates for the current caller. Read state is personal to this
    account and support audience. Private notes never create customer notifications.

    Args:
        org (str):
        before (int | Unset):
        unread (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SupportNotificationPage
    """

    return (
        await asyncio_detailed(
            org=org,
            client=client,
            before=before,
            unread=unread,
        )
    ).parsed
