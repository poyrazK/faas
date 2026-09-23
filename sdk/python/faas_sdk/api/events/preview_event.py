from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.preview_event_request import PreviewEventRequest
from ...models.preview_event_response import PreviewEventResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: PreviewEventRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/events:preview",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PreviewEventResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PreviewEventResponse.from_dict(response.json())

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
) -> Response[PreviewEventResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: PreviewEventRequest,
) -> Response[PreviewEventResponse | Problem]:
    """Preview event routing without publishing.

     Evaluates an event against the authenticated account's enabled
    subscriptions using the same matcher as asynchronous fanout. The
    preview is read-only: it does not persist the event or enqueue work.
    Counts cover every source/type candidate; response lists are bounded
    samples and `truncated` is true when either sample omits candidates.

    Args:
        body (PreviewEventRequest): Event fields to evaluate without persisting or delivering the
            event.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PreviewEventResponse | Problem]
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
    client: AuthenticatedClient,
    body: PreviewEventRequest,
) -> PreviewEventResponse | Problem | None:
    """Preview event routing without publishing.

     Evaluates an event against the authenticated account's enabled
    subscriptions using the same matcher as asynchronous fanout. The
    preview is read-only: it does not persist the event or enqueue work.
    Counts cover every source/type candidate; response lists are bounded
    samples and `truncated` is true when either sample omits candidates.

    Args:
        body (PreviewEventRequest): Event fields to evaluate without persisting or delivering the
            event.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PreviewEventResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: PreviewEventRequest,
) -> Response[PreviewEventResponse | Problem]:
    """Preview event routing without publishing.

     Evaluates an event against the authenticated account's enabled
    subscriptions using the same matcher as asynchronous fanout. The
    preview is read-only: it does not persist the event or enqueue work.
    Counts cover every source/type candidate; response lists are bounded
    samples and `truncated` is true when either sample omits candidates.

    Args:
        body (PreviewEventRequest): Event fields to evaluate without persisting or delivering the
            event.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PreviewEventResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: PreviewEventRequest,
) -> PreviewEventResponse | Problem | None:
    """Preview event routing without publishing.

     Evaluates an event against the authenticated account's enabled
    subscriptions using the same matcher as asynchronous fanout. The
    preview is read-only: it does not persist the event or enqueue work.
    Counts cover every source/type candidate; response lists are bounded
    samples and `truncated` is true when either sample omits candidates.

    Args:
        body (PreviewEventRequest): Event fields to evaluate without persisting or delivering the
            event.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PreviewEventResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
