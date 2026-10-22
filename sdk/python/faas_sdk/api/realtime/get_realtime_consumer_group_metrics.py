from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.realtime_consumer_group_metrics import RealtimeConsumerGroupMetrics
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/metrics".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            group=quote(str(group), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | RealtimeConsumerGroupMetrics | None:
    if response.status_code == 200:
        response_200 = RealtimeConsumerGroupMetrics.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 429:
        response_429 = cast(Any, None)
        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | RealtimeConsumerGroupMetrics]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
) -> Response[Any | RealtimeConsumerGroupMetrics]:
    """Read consumer group processing trends

     Account-owned read with MFA. Returns 60 UTC minute buckets including the partial current minute.
    Queue wait averages publication to first claim, excluding pre-migration attempted snapshots.
    Processing averages successful attempts with an observed lease start, includes renewals, and
    excludes previous failed attempts. Counts are decimal strings. Zero averages with zero samples mean
    no observations. Retries count leased-to-ready transitions including expiry; dead letters count
    entries into dead including deadlines. Replays can add further transitions. Events are bucketed when
    observed, not when an unobserved lease expired. Empty minutes are zero-filled. No per-message
    telemetry is retained.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeConsumerGroupMetrics]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
) -> Any | RealtimeConsumerGroupMetrics | None:
    """Read consumer group processing trends

     Account-owned read with MFA. Returns 60 UTC minute buckets including the partial current minute.
    Queue wait averages publication to first claim, excluding pre-migration attempted snapshots.
    Processing averages successful attempts with an observed lease start, includes renewals, and
    excludes previous failed attempts. Counts are decimal strings. Zero averages with zero samples mean
    no observations. Retries count leased-to-ready transitions including expiry; dead letters count
    entries into dead including deadlines. Replays can add further transitions. Events are bucketed when
    observed, not when an unobserved lease expired. Empty minutes are zero-filled. No per-message
    telemetry is retained.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeConsumerGroupMetrics
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
) -> Response[Any | RealtimeConsumerGroupMetrics]:
    """Read consumer group processing trends

     Account-owned read with MFA. Returns 60 UTC minute buckets including the partial current minute.
    Queue wait averages publication to first claim, excluding pre-migration attempted snapshots.
    Processing averages successful attempts with an observed lease start, includes renewals, and
    excludes previous failed attempts. Counts are decimal strings. Zero averages with zero samples mean
    no observations. Retries count leased-to-ready transitions including expiry; dead letters count
    entries into dead including deadlines. Replays can add further transitions. Events are bucketed when
    observed, not when an unobserved lease expired. Empty minutes are zero-filled. No per-message
    telemetry is retained.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | RealtimeConsumerGroupMetrics]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
) -> Any | RealtimeConsumerGroupMetrics | None:
    """Read consumer group processing trends

     Account-owned read with MFA. Returns 60 UTC minute buckets including the partial current minute.
    Queue wait averages publication to first claim, excluding pre-migration attempted snapshots.
    Processing averages successful attempts with an observed lease start, includes renewals, and
    excludes previous failed attempts. Counts are decimal strings. Zero averages with zero samples mean
    no observations. Retries count leased-to-ready transitions including expiry; dead letters count
    entries into dead including deadlines. Replays can add further transitions. Events are bucketed when
    observed, not when an unobserved lease expired. Empty minutes are zero-filled. No per-message
    telemetry is retained.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | RealtimeConsumerGroupMetrics
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            group=group,
            client=client,
        )
    ).parsed
