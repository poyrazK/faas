from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_notification_channel_request import CreateNotificationChannelRequest
from ...models.notification_channel_response import NotificationChannelResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CreateNotificationChannelRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/notification-channels",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> NotificationChannelResponse | Problem | None:
    if response.status_code == 201:
        response_201 = NotificationChannelResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
    *,
    client: AuthenticatedClient | Client,
    body: CreateNotificationChannelRequest,
) -> Response[NotificationChannelResponse | Problem]:
    """Add an alert notification channel (ADR-749)

     Slack takes an incoming-webhook URL on hooks.slack.com; PagerDuty takes an Events API v2 routing key
    and region; email may only address the account's own email. Secret destinations are sealed at rest
    and never returned.

    Args:
        body (CreateNotificationChannelRequest): Adds a Slack, PagerDuty or email alert channel
            (ADR-749). Set only the fields for the chosen kind.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[NotificationChannelResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: CreateNotificationChannelRequest,
) -> NotificationChannelResponse | Problem | None:
    """Add an alert notification channel (ADR-749)

     Slack takes an incoming-webhook URL on hooks.slack.com; PagerDuty takes an Events API v2 routing key
    and region; email may only address the account's own email. Secret destinations are sealed at rest
    and never returned.

    Args:
        body (CreateNotificationChannelRequest): Adds a Slack, PagerDuty or email alert channel
            (ADR-749). Set only the fields for the chosen kind.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        NotificationChannelResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateNotificationChannelRequest,
) -> Response[NotificationChannelResponse | Problem]:
    """Add an alert notification channel (ADR-749)

     Slack takes an incoming-webhook URL on hooks.slack.com; PagerDuty takes an Events API v2 routing key
    and region; email may only address the account's own email. Secret destinations are sealed at rest
    and never returned.

    Args:
        body (CreateNotificationChannelRequest): Adds a Slack, PagerDuty or email alert channel
            (ADR-749). Set only the fields for the chosen kind.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[NotificationChannelResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreateNotificationChannelRequest,
) -> NotificationChannelResponse | Problem | None:
    """Add an alert notification channel (ADR-749)

     Slack takes an incoming-webhook URL on hooks.slack.com; PagerDuty takes an Events API v2 routing key
    and region; email may only address the account's own email. Secret destinations are sealed at rest
    and never returned.

    Args:
        body (CreateNotificationChannelRequest): Adds a Slack, PagerDuty or email alert channel
            (ADR-749). Set only the fields for the chosen kind.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        NotificationChannelResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
