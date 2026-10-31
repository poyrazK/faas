from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_health_gate import RouteHealthGate
from ...models.set_route_health_gate_request import SetRouteHealthGateRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: SetRouteHealthGateRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/route-health/gate".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteHealthGate | None:
    if response.status_code == 200:
        response_200 = RouteHealthGate.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RouteHealthGate]:
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
    body: SetRouteHealthGateRequest,
) -> Response[Problem | RouteHealthGate]:
    """Save exact route selectors and guard mode with a revision check.

     Requires deploy:write or admin and completed MFA. Enforce mode requires traffic split and request
    telemetry entitlement, and at least one selected route. Maximum 20 distinct exact normalized
    telemetry method/path selectors. Optional max_p95_ms enables an absolute latency budget;
    check_latency enables the independent relative slowdown check. expected_revision is mandatory; use 0
    initially. Identical configuration is a no-op after checking the revision. Changing selectors,
    latency checks or mode resets the observation anchor. Request body limit is 16 KiB.

    Args:
        slug (str):
        body (SetRouteHealthGateRequest): Replacement critical-route health configuration and the
            revision the caller expects to update.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteHealthGate]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetRouteHealthGateRequest,
) -> Problem | RouteHealthGate | None:
    """Save exact route selectors and guard mode with a revision check.

     Requires deploy:write or admin and completed MFA. Enforce mode requires traffic split and request
    telemetry entitlement, and at least one selected route. Maximum 20 distinct exact normalized
    telemetry method/path selectors. Optional max_p95_ms enables an absolute latency budget;
    check_latency enables the independent relative slowdown check. expected_revision is mandatory; use 0
    initially. Identical configuration is a no-op after checking the revision. Changing selectors,
    latency checks or mode resets the observation anchor. Request body limit is 16 KiB.

    Args:
        slug (str):
        body (SetRouteHealthGateRequest): Replacement critical-route health configuration and the
            revision the caller expects to update.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteHealthGate
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetRouteHealthGateRequest,
) -> Response[Problem | RouteHealthGate]:
    """Save exact route selectors and guard mode with a revision check.

     Requires deploy:write or admin and completed MFA. Enforce mode requires traffic split and request
    telemetry entitlement, and at least one selected route. Maximum 20 distinct exact normalized
    telemetry method/path selectors. Optional max_p95_ms enables an absolute latency budget;
    check_latency enables the independent relative slowdown check. expected_revision is mandatory; use 0
    initially. Identical configuration is a no-op after checking the revision. Changing selectors,
    latency checks or mode resets the observation anchor. Request body limit is 16 KiB.

    Args:
        slug (str):
        body (SetRouteHealthGateRequest): Replacement critical-route health configuration and the
            revision the caller expects to update.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteHealthGate]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetRouteHealthGateRequest,
) -> Problem | RouteHealthGate | None:
    """Save exact route selectors and guard mode with a revision check.

     Requires deploy:write or admin and completed MFA. Enforce mode requires traffic split and request
    telemetry entitlement, and at least one selected route. Maximum 20 distinct exact normalized
    telemetry method/path selectors. Optional max_p95_ms enables an absolute latency budget;
    check_latency enables the independent relative slowdown check. expected_revision is mandatory; use 0
    initially. Identical configuration is a no-op after checking the revision. Changing selectors,
    latency checks or mode resets the observation anchor. Request body limit is 16 KiB.

    Args:
        slug (str):
        body (SetRouteHealthGateRequest): Replacement critical-route health configuration and the
            revision the caller expects to update.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteHealthGate
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
