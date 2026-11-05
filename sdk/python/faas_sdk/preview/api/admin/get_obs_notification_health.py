from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.obs_notification_health import ObsNotificationHealth
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/obs/notification-health",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ObsNotificationHealth | Problem | None:
    if response.status_code == 200:
        response_200 = ObsNotificationHealth.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ObsNotificationHealth | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ObsNotificationHealth | Problem]:
    """Inspect alert notification pipeline evidence.

     Requires operator read scope, MFA and the operator allowlist. Accepts no
    query parameters. Reads bounded Alertmanager alert status and Prometheus
    observations under one shared three-second deadline. Signals distinguish
    fresh zero values from missing, invalid, stale or failed evidence.
    Integration counters cover all receivers, including watchdog traffic;
    counter presence does not imply that an integration is configured.
    increases over ten minutes are extrapolated and can be fractional.
    Healthy signal state describes evidence quality. Overall degraded means
    an observed failure, dropped notification, unhealthy target or reload,
    zero queue capacity or current queue pressure above 80 percent. Partial
    means some evidence is unavailable. This read never confirms delivery
    of a specific episode, human receipt, or external watchdog receipt.
    Receiver configuration, addresses, webhook credentials and silence
    comments are never read or returned. A disabled or invalidly configured
    local Alertmanager reader is explicit. Source failures produce a partial
    HTTP 200 observation with null unavailable values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsNotificationHealth | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> Any | ObsNotificationHealth | Problem | None:
    """Inspect alert notification pipeline evidence.

     Requires operator read scope, MFA and the operator allowlist. Accepts no
    query parameters. Reads bounded Alertmanager alert status and Prometheus
    observations under one shared three-second deadline. Signals distinguish
    fresh zero values from missing, invalid, stale or failed evidence.
    Integration counters cover all receivers, including watchdog traffic;
    counter presence does not imply that an integration is configured.
    increases over ten minutes are extrapolated and can be fractional.
    Healthy signal state describes evidence quality. Overall degraded means
    an observed failure, dropped notification, unhealthy target or reload,
    zero queue capacity or current queue pressure above 80 percent. Partial
    means some evidence is unavailable. This read never confirms delivery
    of a specific episode, human receipt, or external watchdog receipt.
    Receiver configuration, addresses, webhook credentials and silence
    comments are never read or returned. A disabled or invalidly configured
    local Alertmanager reader is explicit. Source failures produce a partial
    HTTP 200 observation with null unavailable values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsNotificationHealth | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ObsNotificationHealth | Problem]:
    """Inspect alert notification pipeline evidence.

     Requires operator read scope, MFA and the operator allowlist. Accepts no
    query parameters. Reads bounded Alertmanager alert status and Prometheus
    observations under one shared three-second deadline. Signals distinguish
    fresh zero values from missing, invalid, stale or failed evidence.
    Integration counters cover all receivers, including watchdog traffic;
    counter presence does not imply that an integration is configured.
    increases over ten minutes are extrapolated and can be fractional.
    Healthy signal state describes evidence quality. Overall degraded means
    an observed failure, dropped notification, unhealthy target or reload,
    zero queue capacity or current queue pressure above 80 percent. Partial
    means some evidence is unavailable. This read never confirms delivery
    of a specific episode, human receipt, or external watchdog receipt.
    Receiver configuration, addresses, webhook credentials and silence
    comments are never read or returned. A disabled or invalidly configured
    local Alertmanager reader is explicit. Source failures produce a partial
    HTTP 200 observation with null unavailable values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsNotificationHealth | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> Any | ObsNotificationHealth | Problem | None:
    """Inspect alert notification pipeline evidence.

     Requires operator read scope, MFA and the operator allowlist. Accepts no
    query parameters. Reads bounded Alertmanager alert status and Prometheus
    observations under one shared three-second deadline. Signals distinguish
    fresh zero values from missing, invalid, stale or failed evidence.
    Integration counters cover all receivers, including watchdog traffic;
    counter presence does not imply that an integration is configured.
    increases over ten minutes are extrapolated and can be fractional.
    Healthy signal state describes evidence quality. Overall degraded means
    an observed failure, dropped notification, unhealthy target or reload,
    zero queue capacity or current queue pressure above 80 percent. Partial
    means some evidence is unavailable. This read never confirms delivery
    of a specific episode, human receipt, or external watchdog receipt.
    Receiver configuration, addresses, webhook credentials and silence
    comments are never read or returned. A disabled or invalidly configured
    local Alertmanager reader is explicit. Source failures produce a partial
    HTTP 200 observation with null unavailable values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsNotificationHealth | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
