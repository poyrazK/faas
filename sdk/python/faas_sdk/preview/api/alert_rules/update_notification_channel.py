from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.notification_channel_response import NotificationChannelResponse
from ...models.problem import Problem
from ...models.update_notification_channel_request import UpdateNotificationChannelRequest
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: UpdateNotificationChannelRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/notification-channels/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> NotificationChannelResponse | Problem | None:
    if response.status_code == 200:
        response_200 = NotificationChannelResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[NotificationChannelResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateNotificationChannelRequest,
) -> Response[NotificationChannelResponse | Problem]:
    """Rename a notification channel or replace its destination

     Renames the channel or replaces its Slack webhook URL or PagerDuty routing key in place, so a leaked
    destination is rotated without unbinding the channel from its rules. The kind never changes; an
    email channel can only be renamed. A PagerDuty region changes only together with a new routing key.
    A new destination clears the channel's last error.

    Args:
        id (UUID):
        body (UpdateNotificationChannelRequest): Renames a channel or replaces its destination;
            set name, the channel's own destination, or both.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[NotificationChannelResponse | Problem]
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
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateNotificationChannelRequest,
) -> NotificationChannelResponse | Problem | None:
    """Rename a notification channel or replace its destination

     Renames the channel or replaces its Slack webhook URL or PagerDuty routing key in place, so a leaked
    destination is rotated without unbinding the channel from its rules. The kind never changes; an
    email channel can only be renamed. A PagerDuty region changes only together with a new routing key.
    A new destination clears the channel's last error.

    Args:
        id (UUID):
        body (UpdateNotificationChannelRequest): Renames a channel or replaces its destination;
            set name, the channel's own destination, or both.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        NotificationChannelResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateNotificationChannelRequest,
) -> Response[NotificationChannelResponse | Problem]:
    """Rename a notification channel or replace its destination

     Renames the channel or replaces its Slack webhook URL or PagerDuty routing key in place, so a leaked
    destination is rotated without unbinding the channel from its rules. The kind never changes; an
    email channel can only be renamed. A PagerDuty region changes only together with a new routing key.
    A new destination clears the channel's last error.

    Args:
        id (UUID):
        body (UpdateNotificationChannelRequest): Renames a channel or replaces its destination;
            set name, the channel's own destination, or both.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[NotificationChannelResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateNotificationChannelRequest,
) -> NotificationChannelResponse | Problem | None:
    """Rename a notification channel or replace its destination

     Renames the channel or replaces its Slack webhook URL or PagerDuty routing key in place, so a leaked
    destination is rotated without unbinding the channel from its rules. The kind never changes; an
    email channel can only be renamed. A PagerDuty region changes only together with a new routing key.
    A new destination clears the channel's last error.

    Args:
        id (UUID):
        body (UpdateNotificationChannelRequest): Renames a channel or replaces its destination;
            set name, the channel's own destination, or both.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        NotificationChannelResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
