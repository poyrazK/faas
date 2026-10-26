from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_notification_control_response import ManagedRealtimeNotificationControlResponse
from ...models.managed_realtime_notification_reschedule_request import ManagedRealtimeNotificationRescheduleRequest
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    body: ManagedRealtimeNotificationRescheduleRequest,
    principal: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["principal"] = principal

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/push/notifications/{message_id}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            message_id=quote(str(message_id), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ManagedRealtimeNotificationControlResponse | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeNotificationControlResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ManagedRealtimeNotificationControlResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeNotificationRescheduleRequest,
    principal: str,
) -> Response[Any | ManagedRealtimeNotificationControlResponse]:
    """Reschedule active notifications across devices without reviving completed deliveries

    Args:
        slug (str):
        id (UUID):
        message_id (str):
        principal (str):
        body (ManagedRealtimeNotificationRescheduleRequest): Replacement not-before instant for
            pending notification delivery work.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ManagedRealtimeNotificationControlResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        message_id=message_id,
        body=body,
        principal=principal,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeNotificationRescheduleRequest,
    principal: str,
) -> Any | ManagedRealtimeNotificationControlResponse | None:
    """Reschedule active notifications across devices without reviving completed deliveries

    Args:
        slug (str):
        id (UUID):
        message_id (str):
        principal (str):
        body (ManagedRealtimeNotificationRescheduleRequest): Replacement not-before instant for
            pending notification delivery work.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ManagedRealtimeNotificationControlResponse
    """

    return sync_detailed(
        slug=slug,
        id=id,
        message_id=message_id,
        client=client,
        body=body,
        principal=principal,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeNotificationRescheduleRequest,
    principal: str,
) -> Response[Any | ManagedRealtimeNotificationControlResponse]:
    """Reschedule active notifications across devices without reviving completed deliveries

    Args:
        slug (str):
        id (UUID):
        message_id (str):
        principal (str):
        body (ManagedRealtimeNotificationRescheduleRequest): Replacement not-before instant for
            pending notification delivery work.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ManagedRealtimeNotificationControlResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        message_id=message_id,
        body=body,
        principal=principal,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeNotificationRescheduleRequest,
    principal: str,
) -> Any | ManagedRealtimeNotificationControlResponse | None:
    """Reschedule active notifications across devices without reviving completed deliveries

    Args:
        slug (str):
        id (UUID):
        message_id (str):
        principal (str):
        body (ManagedRealtimeNotificationRescheduleRequest): Replacement not-before instant for
            pending notification delivery work.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ManagedRealtimeNotificationControlResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            message_id=message_id,
            client=client,
            body=body,
            principal=principal,
        )
    ).parsed
