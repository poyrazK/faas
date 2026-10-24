from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.outbound_ai_route_canary import OutboundAIRouteCanary
from ...models.problem import Problem
from ...models.put_outbound_ai_route_canary_request import PutOutboundAIRouteCanaryRequest
from ...types import Response


def _get_kwargs(
    route: UUID,
    *,
    body: PutOutboundAIRouteCanaryRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/outbound/ai-routes/{route}/canary".format(
            route=quote(str(route), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundAIRouteCanary | Problem | None:
    if response.status_code == 200:
        response_200 = OutboundAIRouteCanary.from_dict(response.json())

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
) -> Response[OutboundAIRouteCanary | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    route: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundAIRouteCanaryRequest,
) -> Response[OutboundAIRouteCanary | Problem]:
    """Start or replace an AI route's model canary.

     Requires MFA and deploy-write scope. The canary target receives the given share of the route's calls
    first and is rolled back by outboundd when its error rate or time to first byte is worse than the
    first target's by more than the margins (ADR-1340). Replacing a canary clears its counts.

    Args:
        route (UUID):
        body (PutOutboundAIRouteCanaryRequest): Start or replace a route's model canary. Omitted
            margins take their defaults: 5 error-rate points, 50 calls, 30 minutes, and no latency
            margin.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIRouteCanary | Problem]
    """

    kwargs = _get_kwargs(
        route=route,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    route: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundAIRouteCanaryRequest,
) -> OutboundAIRouteCanary | Problem | None:
    """Start or replace an AI route's model canary.

     Requires MFA and deploy-write scope. The canary target receives the given share of the route's calls
    first and is rolled back by outboundd when its error rate or time to first byte is worse than the
    first target's by more than the margins (ADR-1340). Replacing a canary clears its counts.

    Args:
        route (UUID):
        body (PutOutboundAIRouteCanaryRequest): Start or replace a route's model canary. Omitted
            margins take their defaults: 5 error-rate points, 50 calls, 30 minutes, and no latency
            margin.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIRouteCanary | Problem
    """

    return sync_detailed(
        route=route,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    route: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundAIRouteCanaryRequest,
) -> Response[OutboundAIRouteCanary | Problem]:
    """Start or replace an AI route's model canary.

     Requires MFA and deploy-write scope. The canary target receives the given share of the route's calls
    first and is rolled back by outboundd when its error rate or time to first byte is worse than the
    first target's by more than the margins (ADR-1340). Replacing a canary clears its counts.

    Args:
        route (UUID):
        body (PutOutboundAIRouteCanaryRequest): Start or replace a route's model canary. Omitted
            margins take their defaults: 5 error-rate points, 50 calls, 30 minutes, and no latency
            margin.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIRouteCanary | Problem]
    """

    kwargs = _get_kwargs(
        route=route,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    route: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundAIRouteCanaryRequest,
) -> OutboundAIRouteCanary | Problem | None:
    """Start or replace an AI route's model canary.

     Requires MFA and deploy-write scope. The canary target receives the given share of the route's calls
    first and is rolled back by outboundd when its error rate or time to first byte is worse than the
    first target's by more than the margins (ADR-1340). Replacing a canary clears its counts.

    Args:
        route (UUID):
        body (PutOutboundAIRouteCanaryRequest): Start or replace a route's model canary. Omitted
            margins take their defaults: 5 error-rate points, 50 calls, 30 minutes, and no latency
            margin.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIRouteCanary | Problem
    """

    return (
        await asyncio_detailed(
            route=route,
            client=client,
            body=body,
        )
    ).parsed
