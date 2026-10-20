from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_monitor_config import RouteMonitorConfig
from ...models.set_route_monitor_request import SetRouteMonitorRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: SetRouteMonitorRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/route-monitor".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteMonitorConfig | None:
    if response.status_code == 200:
        response_200 = RouteMonitorConfig.from_dict(response.json())

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
) -> Response[Problem | RouteMonitorConfig]:
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
    body: SetRouteMonitorRequest,
) -> Response[Problem | RouteMonitorConfig]:
    """Save advisory production route budgets with a revision check.

     Requires deployment write access and completed MFA. Enabling requires request telemetry entitlement
    and routes with absolute budgets. customer_group_by optionally evaluates the same budgets per
    request-time tenant or API consumer. Replacement intent requires expected_revision; identical intent
    is a no-op. Changed intent supersedes an open incident without claiming recovery and requires fresh
    windows. Disabled intent remains writable after a downgrade. Body limit is 16 KiB.

    Args:
        slug (str):
        body (SetRouteMonitorRequest): Replacement production monitor intent, with a mandatory
            revision check.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteMonitorConfig]
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
    body: SetRouteMonitorRequest,
) -> Problem | RouteMonitorConfig | None:
    """Save advisory production route budgets with a revision check.

     Requires deployment write access and completed MFA. Enabling requires request telemetry entitlement
    and routes with absolute budgets. customer_group_by optionally evaluates the same budgets per
    request-time tenant or API consumer. Replacement intent requires expected_revision; identical intent
    is a no-op. Changed intent supersedes an open incident without claiming recovery and requires fresh
    windows. Disabled intent remains writable after a downgrade. Body limit is 16 KiB.

    Args:
        slug (str):
        body (SetRouteMonitorRequest): Replacement production monitor intent, with a mandatory
            revision check.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteMonitorConfig
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
    body: SetRouteMonitorRequest,
) -> Response[Problem | RouteMonitorConfig]:
    """Save advisory production route budgets with a revision check.

     Requires deployment write access and completed MFA. Enabling requires request telemetry entitlement
    and routes with absolute budgets. customer_group_by optionally evaluates the same budgets per
    request-time tenant or API consumer. Replacement intent requires expected_revision; identical intent
    is a no-op. Changed intent supersedes an open incident without claiming recovery and requires fresh
    windows. Disabled intent remains writable after a downgrade. Body limit is 16 KiB.

    Args:
        slug (str):
        body (SetRouteMonitorRequest): Replacement production monitor intent, with a mandatory
            revision check.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteMonitorConfig]
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
    body: SetRouteMonitorRequest,
) -> Problem | RouteMonitorConfig | None:
    """Save advisory production route budgets with a revision check.

     Requires deployment write access and completed MFA. Enabling requires request telemetry entitlement
    and routes with absolute budgets. customer_group_by optionally evaluates the same budgets per
    request-time tenant or API consumer. Replacement intent requires expected_revision; identical intent
    is a no-op. Changed intent supersedes an open incident without claiming recovery and requires fresh
    windows. Disabled intent remains writable after a downgrade. Body limit is 16 KiB.

    Args:
        slug (str):
        body (SetRouteMonitorRequest): Replacement production monitor intent, with a mandatory
            revision check.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteMonitorConfig
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
