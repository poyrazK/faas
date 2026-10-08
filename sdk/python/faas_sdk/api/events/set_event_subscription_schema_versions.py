from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_subscription_schema_versions_request import EventSubscriptionSchemaVersionsRequest
from ...models.event_subscription_schema_versions_response import EventSubscriptionSchemaVersionsResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    subscription_id: UUID,
    *,
    body: EventSubscriptionSchemaVersionsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/event-subscriptions/{subscription_id}/schema-versions".format(
            slug=quote(str(slug), safe=""),
            subscription_id=quote(str(subscription_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventSubscriptionSchemaVersionsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EventSubscriptionSchemaVersionsResponse.from_dict(response.json())

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
) -> Response[EventSubscriptionSchemaVersionsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    body: EventSubscriptionSchemaVersionsRequest,
) -> Response[EventSubscriptionSchemaVersionsResponse | Problem]:
    """Replace schema version selection for future events.

     Requires `deploy:write` or `admin`. Captured events retain their acceptance-time selection. An empty
    selection accepts all versions, including unversioned envelopes. Only current app subscriptions can
    be configured.

    Args:
        slug (str):
        subscription_id (UUID):
        body (EventSubscriptionSchemaVersionsRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSubscriptionSchemaVersionsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    body: EventSubscriptionSchemaVersionsRequest,
) -> EventSubscriptionSchemaVersionsResponse | Problem | None:
    """Replace schema version selection for future events.

     Requires `deploy:write` or `admin`. Captured events retain their acceptance-time selection. An empty
    selection accepts all versions, including unversioned envelopes. Only current app subscriptions can
    be configured.

    Args:
        slug (str):
        subscription_id (UUID):
        body (EventSubscriptionSchemaVersionsRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSubscriptionSchemaVersionsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        subscription_id=subscription_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    body: EventSubscriptionSchemaVersionsRequest,
) -> Response[EventSubscriptionSchemaVersionsResponse | Problem]:
    """Replace schema version selection for future events.

     Requires `deploy:write` or `admin`. Captured events retain their acceptance-time selection. An empty
    selection accepts all versions, including unversioned envelopes. Only current app subscriptions can
    be configured.

    Args:
        slug (str):
        subscription_id (UUID):
        body (EventSubscriptionSchemaVersionsRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSubscriptionSchemaVersionsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        subscription_id=subscription_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    subscription_id: UUID,
    *,
    client: AuthenticatedClient,
    body: EventSubscriptionSchemaVersionsRequest,
) -> EventSubscriptionSchemaVersionsResponse | Problem | None:
    """Replace schema version selection for future events.

     Requires `deploy:write` or `admin`. Captured events retain their acceptance-time selection. An empty
    selection accepts all versions, including unversioned envelopes. Only current app subscriptions can
    be configured.

    Args:
        slug (str):
        subscription_id (UUID):
        body (EventSubscriptionSchemaVersionsRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSubscriptionSchemaVersionsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            subscription_id=subscription_id,
            client=client,
            body=body,
        )
    ).parsed
