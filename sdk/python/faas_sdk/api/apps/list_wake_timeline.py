import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.wake_timeline_response import WakeTimelineResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    wake_id: str,
    *,
    since: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 200,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/wakes/{wake_id}/timeline".format(
            slug=quote(str(slug), safe=""),
            wake_id=quote(str(wake_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WakeTimelineResponse | None:
    if response.status_code == 200:
        response_200 = WakeTimelineResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

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
) -> Response[Problem | WakeTimelineResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    wake_id: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 200,
) -> Response[Problem | WakeTimelineResponse]:
    """List the canonical wake-timeline frames for one wake.

     Oldest-first (forward narrative). Returns every typed
    `wake.*` events row for the given wake_id: queue_accepted
    → admitted → boot_started → boot_completed →
    readiness_200 → proxy_first_byte. Build and deploy
    failures (`wake.build_failed`, `wake.deploy_failed`,
    `wake.boot_failed`) are joined in alongside the success
    path so a single GET shows the whole lifecycle.

    For `wake.proxy_first_byte`, `data.latency_ms` is measured from
    request/queue acceptance through the first upstream byte. New rows
    also include `data.proxy_latency_ms` for the final bridge hop. Rows
    written before this contract correction contain the former
    proxy-only value in `latency_ms` and omit `proxy_latency_ms`.

    The endpoint is a sub-resource of `/v1/apps/{slug}`;
    auth and rate-limit share the §12 per-app budget with
    logs/metrics/wake. Cross-account access 404s the
    same way unknown slugs do (forge-proof: every row's
    `data.app_id` is verified to match the resolved app).
    Wake narratives are a Hobby+ observability surface; Free
    accounts receive 402 before slug or wake lookup.

    Args:
        slug (str):
        wake_id (str):
        since (datetime.datetime | Unset):
        limit (int | Unset):  Default: 200.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WakeTimelineResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        wake_id=wake_id,
        since=since,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    wake_id: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 200,
) -> Problem | WakeTimelineResponse | None:
    """List the canonical wake-timeline frames for one wake.

     Oldest-first (forward narrative). Returns every typed
    `wake.*` events row for the given wake_id: queue_accepted
    → admitted → boot_started → boot_completed →
    readiness_200 → proxy_first_byte. Build and deploy
    failures (`wake.build_failed`, `wake.deploy_failed`,
    `wake.boot_failed`) are joined in alongside the success
    path so a single GET shows the whole lifecycle.

    For `wake.proxy_first_byte`, `data.latency_ms` is measured from
    request/queue acceptance through the first upstream byte. New rows
    also include `data.proxy_latency_ms` for the final bridge hop. Rows
    written before this contract correction contain the former
    proxy-only value in `latency_ms` and omit `proxy_latency_ms`.

    The endpoint is a sub-resource of `/v1/apps/{slug}`;
    auth and rate-limit share the §12 per-app budget with
    logs/metrics/wake. Cross-account access 404s the
    same way unknown slugs do (forge-proof: every row's
    `data.app_id` is verified to match the resolved app).
    Wake narratives are a Hobby+ observability surface; Free
    accounts receive 402 before slug or wake lookup.

    Args:
        slug (str):
        wake_id (str):
        since (datetime.datetime | Unset):
        limit (int | Unset):  Default: 200.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WakeTimelineResponse
    """

    return sync_detailed(
        slug=slug,
        wake_id=wake_id,
        client=client,
        since=since,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    wake_id: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 200,
) -> Response[Problem | WakeTimelineResponse]:
    """List the canonical wake-timeline frames for one wake.

     Oldest-first (forward narrative). Returns every typed
    `wake.*` events row for the given wake_id: queue_accepted
    → admitted → boot_started → boot_completed →
    readiness_200 → proxy_first_byte. Build and deploy
    failures (`wake.build_failed`, `wake.deploy_failed`,
    `wake.boot_failed`) are joined in alongside the success
    path so a single GET shows the whole lifecycle.

    For `wake.proxy_first_byte`, `data.latency_ms` is measured from
    request/queue acceptance through the first upstream byte. New rows
    also include `data.proxy_latency_ms` for the final bridge hop. Rows
    written before this contract correction contain the former
    proxy-only value in `latency_ms` and omit `proxy_latency_ms`.

    The endpoint is a sub-resource of `/v1/apps/{slug}`;
    auth and rate-limit share the §12 per-app budget with
    logs/metrics/wake. Cross-account access 404s the
    same way unknown slugs do (forge-proof: every row's
    `data.app_id` is verified to match the resolved app).
    Wake narratives are a Hobby+ observability surface; Free
    accounts receive 402 before slug or wake lookup.

    Args:
        slug (str):
        wake_id (str):
        since (datetime.datetime | Unset):
        limit (int | Unset):  Default: 200.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WakeTimelineResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        wake_id=wake_id,
        since=since,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    wake_id: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 200,
) -> Problem | WakeTimelineResponse | None:
    """List the canonical wake-timeline frames for one wake.

     Oldest-first (forward narrative). Returns every typed
    `wake.*` events row for the given wake_id: queue_accepted
    → admitted → boot_started → boot_completed →
    readiness_200 → proxy_first_byte. Build and deploy
    failures (`wake.build_failed`, `wake.deploy_failed`,
    `wake.boot_failed`) are joined in alongside the success
    path so a single GET shows the whole lifecycle.

    For `wake.proxy_first_byte`, `data.latency_ms` is measured from
    request/queue acceptance through the first upstream byte. New rows
    also include `data.proxy_latency_ms` for the final bridge hop. Rows
    written before this contract correction contain the former
    proxy-only value in `latency_ms` and omit `proxy_latency_ms`.

    The endpoint is a sub-resource of `/v1/apps/{slug}`;
    auth and rate-limit share the §12 per-app budget with
    logs/metrics/wake. Cross-account access 404s the
    same way unknown slugs do (forge-proof: every row's
    `data.app_id` is verified to match the resolved app).
    Wake narratives are a Hobby+ observability surface; Free
    accounts receive 402 before slug or wake lookup.

    Args:
        slug (str):
        wake_id (str):
        since (datetime.datetime | Unset):
        limit (int | Unset):  Default: 200.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WakeTimelineResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            wake_id=wake_id,
            client=client,
            since=since,
            limit=limit,
        )
    ).parsed
