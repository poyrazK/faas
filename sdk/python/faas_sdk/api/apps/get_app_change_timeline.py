import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.app_change_timeline_response import AppChangeTimelineResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    json_until: str | Unset = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/changes".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppChangeTimelineResponse | Problem:
    if response.status_code == 200:
        response_200 = AppChangeTimelineResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppChangeTimelineResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> Response[AppChangeTimelineResponse | Problem]:
    """Read an app's change timeline.

     ADR-741 internal preview, enabled by `FAAS_CHANGE_TIMELINE_ENABLED=1`;
    otherwise 503 `change_timeline_unavailable`. Requires apps:read or
    admin; no MFA required.

    Merges deployment audit, edge-rule changes, the latest runtime-config
    change, route incidents, recorded health transitions, and environment
    and domain activity into one newest-first list. The window defaults to
    the last 24 hours, `until` is clamped to now, and the window is at most
    7 days. At most 200 events are returned (`truncated: true` when more
    exist). A source that cannot be read is named in `unavailable_sources`
    and the remaining sources are still returned. Summaries never contain
    values, credentials, actors, or raw error text.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppChangeTimelineResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> AppChangeTimelineResponse | Problem | None:
    """Read an app's change timeline.

     ADR-741 internal preview, enabled by `FAAS_CHANGE_TIMELINE_ENABLED=1`;
    otherwise 503 `change_timeline_unavailable`. Requires apps:read or
    admin; no MFA required.

    Merges deployment audit, edge-rule changes, the latest runtime-config
    change, route incidents, recorded health transitions, and environment
    and domain activity into one newest-first list. The window defaults to
    the last 24 hours, `until` is clamped to now, and the window is at most
    7 days. At most 200 events are returned (`truncated: true` when more
    exist). A source that cannot be read is named in `unavailable_sources`
    and the remaining sources are still returned. Summaries never contain
    values, credentials, actors, or raw error text.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppChangeTimelineResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        since=since,
        until=until,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> Response[AppChangeTimelineResponse | Problem]:
    """Read an app's change timeline.

     ADR-741 internal preview, enabled by `FAAS_CHANGE_TIMELINE_ENABLED=1`;
    otherwise 503 `change_timeline_unavailable`. Requires apps:read or
    admin; no MFA required.

    Merges deployment audit, edge-rule changes, the latest runtime-config
    change, route incidents, recorded health transitions, and environment
    and domain activity into one newest-first list. The window defaults to
    the last 24 hours, `until` is clamped to now, and the window is at most
    7 days. At most 200 events are returned (`truncated: true` when more
    exist). A source that cannot be read is named in `unavailable_sources`
    and the remaining sources are still returned. Summaries never contain
    values, credentials, actors, or raw error text.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppChangeTimelineResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> AppChangeTimelineResponse | Problem | None:
    """Read an app's change timeline.

     ADR-741 internal preview, enabled by `FAAS_CHANGE_TIMELINE_ENABLED=1`;
    otherwise 503 `change_timeline_unavailable`. Requires apps:read or
    admin; no MFA required.

    Merges deployment audit, edge-rule changes, the latest runtime-config
    change, route incidents, recorded health transitions, and environment
    and domain activity into one newest-first list. The window defaults to
    the last 24 hours, `until` is clamped to now, and the window is at most
    7 days. At most 200 events are returned (`truncated: true` when more
    exist). A source that cannot be read is named in `unavailable_sources`
    and the remaining sources are still returned. Summaries never contain
    values, credentials, actors, or raw error text.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppChangeTimelineResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
            until=until,
        )
    ).parsed
