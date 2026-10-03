from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_monitor_incident import RouteMonitorIncident
from ...types import Response


def _get_kwargs(
    slug: str,
    incident: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/route-monitor/incidents/{incident}".format(
            slug=quote(str(slug), safe=""),
            incident=quote(str(incident), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteMonitorIncident | None:
    if response.status_code == 200:
        response_200 = RouteMonitorIncident.from_dict(response.json())

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
) -> Response[Problem | RouteMonitorIncident]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    incident: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RouteMonitorIncident]:
    """Read the opening evidence and closure of one saved production route incident.

     Requires app read access, completed MFA and current request telemetry entitlement. Opening windows,
    deployment, commit, budgets, request references and dependency summaries are captured when the
    worker opens the incident. Recovered means comparable healthy windows for all selected budgets;
    superseded means context changed and never emits recovery. Debugger links recheck current retention
    and authorization.

    Args:
        slug (str):
        incident (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteMonitorIncident]
    """

    kwargs = _get_kwargs(
        slug=slug,
        incident=incident,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    incident: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RouteMonitorIncident | None:
    """Read the opening evidence and closure of one saved production route incident.

     Requires app read access, completed MFA and current request telemetry entitlement. Opening windows,
    deployment, commit, budgets, request references and dependency summaries are captured when the
    worker opens the incident. Recovered means comparable healthy windows for all selected budgets;
    superseded means context changed and never emits recovery. Debugger links recheck current retention
    and authorization.

    Args:
        slug (str):
        incident (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteMonitorIncident
    """

    return sync_detailed(
        slug=slug,
        incident=incident,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    incident: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RouteMonitorIncident]:
    """Read the opening evidence and closure of one saved production route incident.

     Requires app read access, completed MFA and current request telemetry entitlement. Opening windows,
    deployment, commit, budgets, request references and dependency summaries are captured when the
    worker opens the incident. Recovered means comparable healthy windows for all selected budgets;
    superseded means context changed and never emits recovery. Debugger links recheck current retention
    and authorization.

    Args:
        slug (str):
        incident (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteMonitorIncident]
    """

    kwargs = _get_kwargs(
        slug=slug,
        incident=incident,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    incident: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RouteMonitorIncident | None:
    """Read the opening evidence and closure of one saved production route incident.

     Requires app read access, completed MFA and current request telemetry entitlement. Opening windows,
    deployment, commit, budgets, request references and dependency summaries are captured when the
    worker opens the incident. Recovered means comparable healthy windows for all selected budgets;
    superseded means context changed and never emits recovery. Debugger links recheck current retention
    and authorization.

    Args:
        slug (str):
        incident (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteMonitorIncident
    """

    return (
        await asyncio_detailed(
            slug=slug,
            incident=incident,
            client=client,
        )
    ).parsed
