from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.build_response import BuildResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/builds/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BuildResponse | Problem | None:
    if response.status_code == 200:
        response_200 = BuildResponse.from_dict(response.json())

        return response_200

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
) -> Response[BuildResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[BuildResponse | Problem]:
    """Get build status.

     Returns the lifecycle row for a build id — current status,
    enqueued/started/finished timestamps, failure_class (when
    status='failed'), and a server-computed duration_seconds
    (only set when both started_at and finished_at are
    populated). Companion to /v1/builds/{id}/provenance (post-
    mortem export) and /v1/builds/{id}/sbom (post-mortem
    blob); this one is the LIFECYCLE surface CI scripts call
    to fail-fast on build error without scraping SSE.

    The status enum is `queued|running|succeeded|failed` (the
    builds_status_check CHECK constraint; no 'cancelled' value).
    failure_class, when present, is one of `oom|timeout|
    user_error|infra` (the builds_failure_class_check CHECK
    constraint). error_message is intentionally NOT in the
    response — it lives on deployments; call GET
    /v1/deployments/{id} for the per-failure string.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BuildResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> BuildResponse | Problem | None:
    """Get build status.

     Returns the lifecycle row for a build id — current status,
    enqueued/started/finished timestamps, failure_class (when
    status='failed'), and a server-computed duration_seconds
    (only set when both started_at and finished_at are
    populated). Companion to /v1/builds/{id}/provenance (post-
    mortem export) and /v1/builds/{id}/sbom (post-mortem
    blob); this one is the LIFECYCLE surface CI scripts call
    to fail-fast on build error without scraping SSE.

    The status enum is `queued|running|succeeded|failed` (the
    builds_status_check CHECK constraint; no 'cancelled' value).
    failure_class, when present, is one of `oom|timeout|
    user_error|infra` (the builds_failure_class_check CHECK
    constraint). error_message is intentionally NOT in the
    response — it lives on deployments; call GET
    /v1/deployments/{id} for the per-failure string.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BuildResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[BuildResponse | Problem]:
    """Get build status.

     Returns the lifecycle row for a build id — current status,
    enqueued/started/finished timestamps, failure_class (when
    status='failed'), and a server-computed duration_seconds
    (only set when both started_at and finished_at are
    populated). Companion to /v1/builds/{id}/provenance (post-
    mortem export) and /v1/builds/{id}/sbom (post-mortem
    blob); this one is the LIFECYCLE surface CI scripts call
    to fail-fast on build error without scraping SSE.

    The status enum is `queued|running|succeeded|failed` (the
    builds_status_check CHECK constraint; no 'cancelled' value).
    failure_class, when present, is one of `oom|timeout|
    user_error|infra` (the builds_failure_class_check CHECK
    constraint). error_message is intentionally NOT in the
    response — it lives on deployments; call GET
    /v1/deployments/{id} for the per-failure string.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BuildResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> BuildResponse | Problem | None:
    """Get build status.

     Returns the lifecycle row for a build id — current status,
    enqueued/started/finished timestamps, failure_class (when
    status='failed'), and a server-computed duration_seconds
    (only set when both started_at and finished_at are
    populated). Companion to /v1/builds/{id}/provenance (post-
    mortem export) and /v1/builds/{id}/sbom (post-mortem
    blob); this one is the LIFECYCLE surface CI scripts call
    to fail-fast on build error without scraping SSE.

    The status enum is `queued|running|succeeded|failed` (the
    builds_status_check CHECK constraint; no 'cancelled' value).
    failure_class, when present, is one of `oom|timeout|
    user_error|infra` (the builds_failure_class_check CHECK
    constraint). error_message is intentionally NOT in the
    response — it lives on deployments; call GET
    /v1/deployments/{id} for the per-failure string.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BuildResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
