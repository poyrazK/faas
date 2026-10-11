from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_event_schema_request import ManagedRealtimeEventSchemaRequest
from ...models.managed_realtime_event_schema_response import ManagedRealtimeEventSchemaResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    channel: str,
    event_type: str,
    version: int,
    *,
    body: ManagedRealtimeEventSchemaRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schemas/{event_type}/{version}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            event_type=quote(str(event_type), safe=""),
            version=quote(str(version), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ManagedRealtimeEventSchemaResponse | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeEventSchemaResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 429:
        response_429 = cast(Any, None)
        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ManagedRealtimeEventSchemaResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    channel: str,
    event_type: str,
    version: int,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeEventSchemaRequest,
) -> Response[Any | ManagedRealtimeEventSchemaResponse]:
    """Register an immutable event schema version and enable channel enforcement

    Args:
        slug (str):
        id (UUID):
        channel (str):
        event_type (str):
        version (int):
        body (ManagedRealtimeEventSchemaRequest): JSON Schema document for one channel event type
            and version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ManagedRealtimeEventSchemaResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        event_type=event_type,
        version=version,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    channel: str,
    event_type: str,
    version: int,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeEventSchemaRequest,
) -> Any | ManagedRealtimeEventSchemaResponse | None:
    """Register an immutable event schema version and enable channel enforcement

    Args:
        slug (str):
        id (UUID):
        channel (str):
        event_type (str):
        version (int):
        body (ManagedRealtimeEventSchemaRequest): JSON Schema document for one channel event type
            and version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ManagedRealtimeEventSchemaResponse
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        event_type=event_type,
        version=version,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    channel: str,
    event_type: str,
    version: int,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeEventSchemaRequest,
) -> Response[Any | ManagedRealtimeEventSchemaResponse]:
    """Register an immutable event schema version and enable channel enforcement

    Args:
        slug (str):
        id (UUID):
        channel (str):
        event_type (str):
        version (int):
        body (ManagedRealtimeEventSchemaRequest): JSON Schema document for one channel event type
            and version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ManagedRealtimeEventSchemaResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        event_type=event_type,
        version=version,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    channel: str,
    event_type: str,
    version: int,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeEventSchemaRequest,
) -> Any | ManagedRealtimeEventSchemaResponse | None:
    """Register an immutable event schema version and enable channel enforcement

    Args:
        slug (str):
        id (UUID):
        channel (str):
        event_type (str):
        version (int):
        body (ManagedRealtimeEventSchemaRequest): JSON Schema document for one channel event type
            and version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ManagedRealtimeEventSchemaResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            event_type=event_type,
            version=version,
            client=client,
            body=body,
        )
    ).parsed
