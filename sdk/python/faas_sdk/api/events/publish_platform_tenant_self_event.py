from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_publish_event_response import PlatformTenantPublishEventResponse
from ...models.problem import Problem
from ...models.publish_event_request import PublishEventRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: PublishEventRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/platform-tenant-self/apps/{slug}/events:publish".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantPublishEventResponse | Problem | None:
    if response.status_code == 202:
        response_202 = PlatformTenantPublishEventResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[PlatformTenantPublishEventResponse | Problem]:
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
    body: PublishEventRequest,
) -> Response[PlatformTenantPublishEventResponse | Problem]:
    """Publish an event for an automation linked to this tenant.

     Requires platform_tenant:events:manage. The tenant identity comes from
    the bearer token, and the app must be actively linked to that tenant.
    The platform scopes the event to that app's published event-triggered
    workflows; it does not fan out to other apps' subscriptions. The caller's
    id is scoped by tenant, app, and source, so repeating the same identity
    and content returns the original durable receipt. The response exposes
    both the canonical platform id and the caller's client_event_id.

    Args:
        slug (str):
        body (PublishEventRequest): Caller-authored envelope for the tenant-scoped internal event
            router.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantPublishEventResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: PublishEventRequest,
) -> PlatformTenantPublishEventResponse | Problem | None:
    """Publish an event for an automation linked to this tenant.

     Requires platform_tenant:events:manage. The tenant identity comes from
    the bearer token, and the app must be actively linked to that tenant.
    The platform scopes the event to that app's published event-triggered
    workflows; it does not fan out to other apps' subscriptions. The caller's
    id is scoped by tenant, app, and source, so repeating the same identity
    and content returns the original durable receipt. The response exposes
    both the canonical platform id and the caller's client_event_id.

    Args:
        slug (str):
        body (PublishEventRequest): Caller-authored envelope for the tenant-scoped internal event
            router.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantPublishEventResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: PublishEventRequest,
) -> Response[PlatformTenantPublishEventResponse | Problem]:
    """Publish an event for an automation linked to this tenant.

     Requires platform_tenant:events:manage. The tenant identity comes from
    the bearer token, and the app must be actively linked to that tenant.
    The platform scopes the event to that app's published event-triggered
    workflows; it does not fan out to other apps' subscriptions. The caller's
    id is scoped by tenant, app, and source, so repeating the same identity
    and content returns the original durable receipt. The response exposes
    both the canonical platform id and the caller's client_event_id.

    Args:
        slug (str):
        body (PublishEventRequest): Caller-authored envelope for the tenant-scoped internal event
            router.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantPublishEventResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: PublishEventRequest,
) -> PlatformTenantPublishEventResponse | Problem | None:
    """Publish an event for an automation linked to this tenant.

     Requires platform_tenant:events:manage. The tenant identity comes from
    the bearer token, and the app must be actively linked to that tenant.
    The platform scopes the event to that app's published event-triggered
    workflows; it does not fan out to other apps' subscriptions. The caller's
    id is scoped by tenant, app, and source, so repeating the same identity
    and content returns the original durable receipt. The response exposes
    both the canonical platform id and the caller's client_event_id.

    Args:
        slug (str):
        body (PublishEventRequest): Caller-authored envelope for the tenant-scoped internal event
            router.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantPublishEventResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
