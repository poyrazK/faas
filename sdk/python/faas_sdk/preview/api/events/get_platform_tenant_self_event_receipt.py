from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_receipt_response import EventReceiptResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    event_id: str,
    *,
    source: str,
    limit: int | Unset = 100,
    after: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["source"] = source

    params["limit"] = limit

    params["after"] = after

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/apps/{slug}/events/receipts/{event_id}".format(
            slug=quote(str(slug), safe=""),
            event_id=quote(str(event_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventReceiptResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EventReceiptResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventReceiptResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    event_id: str,
    *,
    client: AuthenticatedClient,
    source: str,
    limit: int | Unset = 100,
    after: str | Unset = UNSET,
) -> Response[EventReceiptResponse | Problem]:
    """Read routing and workflow-start evidence for a tenant event.

     Requires platform_tenant:events:read. Receipts are visible only to the authenticated tenant that
    published the event for this linked app. Account-operator recovery actions are omitted.

    Args:
        slug (str):
        event_id (str):
        source (str):
        limit (int | Unset):  Default: 100.
        after (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventReceiptResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        event_id=event_id,
        source=source,
        limit=limit,
        after=after,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    event_id: str,
    *,
    client: AuthenticatedClient,
    source: str,
    limit: int | Unset = 100,
    after: str | Unset = UNSET,
) -> EventReceiptResponse | Problem | None:
    """Read routing and workflow-start evidence for a tenant event.

     Requires platform_tenant:events:read. Receipts are visible only to the authenticated tenant that
    published the event for this linked app. Account-operator recovery actions are omitted.

    Args:
        slug (str):
        event_id (str):
        source (str):
        limit (int | Unset):  Default: 100.
        after (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventReceiptResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        event_id=event_id,
        client=client,
        source=source,
        limit=limit,
        after=after,
    ).parsed


async def asyncio_detailed(
    slug: str,
    event_id: str,
    *,
    client: AuthenticatedClient,
    source: str,
    limit: int | Unset = 100,
    after: str | Unset = UNSET,
) -> Response[EventReceiptResponse | Problem]:
    """Read routing and workflow-start evidence for a tenant event.

     Requires platform_tenant:events:read. Receipts are visible only to the authenticated tenant that
    published the event for this linked app. Account-operator recovery actions are omitted.

    Args:
        slug (str):
        event_id (str):
        source (str):
        limit (int | Unset):  Default: 100.
        after (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventReceiptResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        event_id=event_id,
        source=source,
        limit=limit,
        after=after,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    event_id: str,
    *,
    client: AuthenticatedClient,
    source: str,
    limit: int | Unset = 100,
    after: str | Unset = UNSET,
) -> EventReceiptResponse | Problem | None:
    """Read routing and workflow-start evidence for a tenant event.

     Requires platform_tenant:events:read. Receipts are visible only to the authenticated tenant that
    published the event for this linked app. Account-operator recovery actions are omitted.

    Args:
        slug (str):
        event_id (str):
        source (str):
        limit (int | Unset):  Default: 100.
        after (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventReceiptResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            event_id=event_id,
            client=client,
            source=source,
            limit=limit,
            after=after,
        )
    ).parsed
