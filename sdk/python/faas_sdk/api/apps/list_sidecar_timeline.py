import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.sidecar_timeline_response import SidecarTimelineResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    sidecar_name: str,
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
        "url": "/v1/apps/{slug}/sidecars/{sidecar_name}/timeline".format(
            slug=quote(str(slug), safe=""),
            sidecar_name=quote(str(sidecar_name), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SidecarTimelineResponse | None:
    if response.status_code == 200:
        response_200 = SidecarTimelineResponse.from_dict(response.json())

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
) -> Response[Problem | SidecarTimelineResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    sidecar_name: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 200,
) -> Response[Problem | SidecarTimelineResponse]:
    """List the lifecycle timeline for one sidecar.

     Oldest-first (forward narrative). Returns the sidecar's init-exit,
    restart, and health-transition frames. The `latest` field is the
    most recent `wake.sidecar_health` status (`starting`, `healthy`,
    `unhealthy`, `restarting`, or `failed`) when one is available.

    The endpoint is a sub-resource of `/v1/apps/{slug}` and uses the
    same MFA, scope, per-app rate-limit, and Hobby+ observability gates
    as the wake timeline. Cross-account rows are dropped by verifying
    every event's `data.app_id` against the slug's resolved app.

    Args:
        slug (str):
        sidecar_name (str):
        since (datetime.datetime | Unset):
        limit (int | Unset):  Default: 200.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SidecarTimelineResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        sidecar_name=sidecar_name,
        since=since,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    sidecar_name: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 200,
) -> Problem | SidecarTimelineResponse | None:
    """List the lifecycle timeline for one sidecar.

     Oldest-first (forward narrative). Returns the sidecar's init-exit,
    restart, and health-transition frames. The `latest` field is the
    most recent `wake.sidecar_health` status (`starting`, `healthy`,
    `unhealthy`, `restarting`, or `failed`) when one is available.

    The endpoint is a sub-resource of `/v1/apps/{slug}` and uses the
    same MFA, scope, per-app rate-limit, and Hobby+ observability gates
    as the wake timeline. Cross-account rows are dropped by verifying
    every event's `data.app_id` against the slug's resolved app.

    Args:
        slug (str):
        sidecar_name (str):
        since (datetime.datetime | Unset):
        limit (int | Unset):  Default: 200.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SidecarTimelineResponse
    """

    return sync_detailed(
        slug=slug,
        sidecar_name=sidecar_name,
        client=client,
        since=since,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    sidecar_name: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 200,
) -> Response[Problem | SidecarTimelineResponse]:
    """List the lifecycle timeline for one sidecar.

     Oldest-first (forward narrative). Returns the sidecar's init-exit,
    restart, and health-transition frames. The `latest` field is the
    most recent `wake.sidecar_health` status (`starting`, `healthy`,
    `unhealthy`, `restarting`, or `failed`) when one is available.

    The endpoint is a sub-resource of `/v1/apps/{slug}` and uses the
    same MFA, scope, per-app rate-limit, and Hobby+ observability gates
    as the wake timeline. Cross-account rows are dropped by verifying
    every event's `data.app_id` against the slug's resolved app.

    Args:
        slug (str):
        sidecar_name (str):
        since (datetime.datetime | Unset):
        limit (int | Unset):  Default: 200.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SidecarTimelineResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        sidecar_name=sidecar_name,
        since=since,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    sidecar_name: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 200,
) -> Problem | SidecarTimelineResponse | None:
    """List the lifecycle timeline for one sidecar.

     Oldest-first (forward narrative). Returns the sidecar's init-exit,
    restart, and health-transition frames. The `latest` field is the
    most recent `wake.sidecar_health` status (`starting`, `healthy`,
    `unhealthy`, `restarting`, or `failed`) when one is available.

    The endpoint is a sub-resource of `/v1/apps/{slug}` and uses the
    same MFA, scope, per-app rate-limit, and Hobby+ observability gates
    as the wake timeline. Cross-account rows are dropped by verifying
    every event's `data.app_id` against the slug's resolved app.

    Args:
        slug (str):
        sidecar_name (str):
        since (datetime.datetime | Unset):
        limit (int | Unset):  Default: 200.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SidecarTimelineResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            sidecar_name=sidecar_name,
            client=client,
            since=since,
            limit=limit,
        )
    ).parsed
