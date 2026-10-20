from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_health_history_entry import RouteHealthHistoryEntry
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
    decision_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/route-health/deployments/{deployment}/history/{decision_id}".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
            decision_id=quote(str(decision_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteHealthHistoryEntry | None:
    if response.status_code == 200:
        response_200 = RouteHealthHistoryEntry.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RouteHealthHistoryEntry]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    decision_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RouteHealthHistoryEntry]:
    """Read the exact observations and thresholds behind a retained rollout decision.

     Requires apps:read or admin and completed MFA. Account, app and deployment scoped. Available after
    plan downgrade and rollout completion. Returns 404 for a missing, foreign or pruned decision. Reads
    never evaluate telemetry or change traffic. Allowed entries committed with the traffic transaction;
    blocked entries preserve only the held evaluation. Later failed traffic transactions leave no
    allowed snapshot.

    Args:
        slug (str):
        deployment (UUID):
        decision_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteHealthHistoryEntry]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        decision_id=decision_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    decision_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RouteHealthHistoryEntry | None:
    """Read the exact observations and thresholds behind a retained rollout decision.

     Requires apps:read or admin and completed MFA. Account, app and deployment scoped. Available after
    plan downgrade and rollout completion. Returns 404 for a missing, foreign or pruned decision. Reads
    never evaluate telemetry or change traffic. Allowed entries committed with the traffic transaction;
    blocked entries preserve only the held evaluation. Later failed traffic transactions leave no
    allowed snapshot.

    Args:
        slug (str):
        deployment (UUID):
        decision_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteHealthHistoryEntry
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        decision_id=decision_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    decision_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RouteHealthHistoryEntry]:
    """Read the exact observations and thresholds behind a retained rollout decision.

     Requires apps:read or admin and completed MFA. Account, app and deployment scoped. Available after
    plan downgrade and rollout completion. Returns 404 for a missing, foreign or pruned decision. Reads
    never evaluate telemetry or change traffic. Allowed entries committed with the traffic transaction;
    blocked entries preserve only the held evaluation. Later failed traffic transactions leave no
    allowed snapshot.

    Args:
        slug (str):
        deployment (UUID):
        decision_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteHealthHistoryEntry]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        decision_id=decision_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    decision_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RouteHealthHistoryEntry | None:
    """Read the exact observations and thresholds behind a retained rollout decision.

     Requires apps:read or admin and completed MFA. Account, app and deployment scoped. Available after
    plan downgrade and rollout completion. Returns 404 for a missing, foreign or pruned decision. Reads
    never evaluate telemetry or change traffic. Allowed entries committed with the traffic transaction;
    blocked entries preserve only the held evaluation. Later failed traffic transactions leave no
    allowed snapshot.

    Args:
        slug (str):
        deployment (UUID):
        decision_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteHealthHistoryEntry
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            decision_id=decision_id,
            client=client,
        )
    ).parsed
