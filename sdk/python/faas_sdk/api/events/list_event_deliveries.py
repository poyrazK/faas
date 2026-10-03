from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_delivery_list_response import EventDeliveryListResponse
from ...models.list_event_deliveries_state import ListEventDeliveriesState
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    event_source: str | Unset = UNSET,
    event_id: str | Unset = UNSET,
    state: ListEventDeliveriesState | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
    fanout_before: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["event_source"] = event_source

    params["event_id"] = event_id

    json_state: str | Unset = UNSET
    if not isinstance(state, Unset):
        json_state = state

    params["state"] = json_state

    params["before"] = before

    params["limit"] = limit

    params["fanout_before"] = fanout_before

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-deliveries".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventDeliveryListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EventDeliveryListResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventDeliveryListResponse | Problem]:
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
    event_source: str | Unset = UNSET,
    event_id: str | Unset = UNSET,
    state: ListEventDeliveriesState | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
    fanout_before: str | Unset = UNSET,
) -> Response[EventDeliveryListResponse | Problem]:
    """Inspect event delivery lifecycle for an app.

     Returns original event-triggered invocation metadata, operator replay
    invocations carrying event identity headers, and terminal fanout
    recipient failures, each newest first. The projections include the
    published event identity, subscription, lifecycle state, attempts,
    and last error without returning payloads. The two histories have
    independent pagination cursors. event_id alone searches across event
    sources; provide event_source with event_id to select one event identity.

    Args:
        slug (str):
        event_source (str | Unset):
        event_id (str | Unset):
        state (ListEventDeliveriesState | Unset):
        before (str | Unset):
        limit (int | Unset):  Default: 20.
        fanout_before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventDeliveryListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        event_source=event_source,
        event_id=event_id,
        state=state,
        before=before,
        limit=limit,
        fanout_before=fanout_before,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    event_source: str | Unset = UNSET,
    event_id: str | Unset = UNSET,
    state: ListEventDeliveriesState | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
    fanout_before: str | Unset = UNSET,
) -> EventDeliveryListResponse | Problem | None:
    """Inspect event delivery lifecycle for an app.

     Returns original event-triggered invocation metadata, operator replay
    invocations carrying event identity headers, and terminal fanout
    recipient failures, each newest first. The projections include the
    published event identity, subscription, lifecycle state, attempts,
    and last error without returning payloads. The two histories have
    independent pagination cursors. event_id alone searches across event
    sources; provide event_source with event_id to select one event identity.

    Args:
        slug (str):
        event_source (str | Unset):
        event_id (str | Unset):
        state (ListEventDeliveriesState | Unset):
        before (str | Unset):
        limit (int | Unset):  Default: 20.
        fanout_before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventDeliveryListResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        event_source=event_source,
        event_id=event_id,
        state=state,
        before=before,
        limit=limit,
        fanout_before=fanout_before,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    event_source: str | Unset = UNSET,
    event_id: str | Unset = UNSET,
    state: ListEventDeliveriesState | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
    fanout_before: str | Unset = UNSET,
) -> Response[EventDeliveryListResponse | Problem]:
    """Inspect event delivery lifecycle for an app.

     Returns original event-triggered invocation metadata, operator replay
    invocations carrying event identity headers, and terminal fanout
    recipient failures, each newest first. The projections include the
    published event identity, subscription, lifecycle state, attempts,
    and last error without returning payloads. The two histories have
    independent pagination cursors. event_id alone searches across event
    sources; provide event_source with event_id to select one event identity.

    Args:
        slug (str):
        event_source (str | Unset):
        event_id (str | Unset):
        state (ListEventDeliveriesState | Unset):
        before (str | Unset):
        limit (int | Unset):  Default: 20.
        fanout_before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventDeliveryListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        event_source=event_source,
        event_id=event_id,
        state=state,
        before=before,
        limit=limit,
        fanout_before=fanout_before,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    event_source: str | Unset = UNSET,
    event_id: str | Unset = UNSET,
    state: ListEventDeliveriesState | Unset = UNSET,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
    fanout_before: str | Unset = UNSET,
) -> EventDeliveryListResponse | Problem | None:
    """Inspect event delivery lifecycle for an app.

     Returns original event-triggered invocation metadata, operator replay
    invocations carrying event identity headers, and terminal fanout
    recipient failures, each newest first. The projections include the
    published event identity, subscription, lifecycle state, attempts,
    and last error without returning payloads. The two histories have
    independent pagination cursors. event_id alone searches across event
    sources; provide event_source with event_id to select one event identity.

    Args:
        slug (str):
        event_source (str | Unset):
        event_id (str | Unset):
        state (ListEventDeliveriesState | Unset):
        before (str | Unset):
        limit (int | Unset):  Default: 20.
        fanout_before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventDeliveryListResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            event_source=event_source,
            event_id=event_id,
            state=state,
            before=before,
            limit=limit,
            fanout_before=fanout_before,
        )
    ).parsed
