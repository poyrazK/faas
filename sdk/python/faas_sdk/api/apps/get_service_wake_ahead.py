from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.service_wake_ahead_response import ServiceWakeAheadResponse
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/service-wake-ahead".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ServiceWakeAheadResponse | None:
    if response.status_code == 200:
        response_200 = ServiceWakeAheadResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ServiceWakeAheadResponse]:
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
) -> Response[Problem | ServiceWakeAheadResponse]:
    """Read an app's service wake-ahead opt-in.

     Service wake-ahead (ADR-956) is off by default. When it is on and this
    app starts a cold wake, the gateway also starts restoring the services
    it has measured this app calling soon after it wakes, so their restores
    overlap instead of running one after another. Measurement is per
    gateway and needs at least 20 observed wakes of this app before any
    service is woken ahead. Woken services are billed like any other
    running instance. Requires app read access and completed MFA.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ServiceWakeAheadResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ServiceWakeAheadResponse | None:
    """Read an app's service wake-ahead opt-in.

     Service wake-ahead (ADR-956) is off by default. When it is on and this
    app starts a cold wake, the gateway also starts restoring the services
    it has measured this app calling soon after it wakes, so their restores
    overlap instead of running one after another. Measurement is per
    gateway and needs at least 20 observed wakes of this app before any
    service is woken ahead. Woken services are billed like any other
    running instance. Requires app read access and completed MFA.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ServiceWakeAheadResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ServiceWakeAheadResponse]:
    """Read an app's service wake-ahead opt-in.

     Service wake-ahead (ADR-956) is off by default. When it is on and this
    app starts a cold wake, the gateway also starts restoring the services
    it has measured this app calling soon after it wakes, so their restores
    overlap instead of running one after another. Measurement is per
    gateway and needs at least 20 observed wakes of this app before any
    service is woken ahead. Woken services are billed like any other
    running instance. Requires app read access and completed MFA.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ServiceWakeAheadResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ServiceWakeAheadResponse | None:
    """Read an app's service wake-ahead opt-in.

     Service wake-ahead (ADR-956) is off by default. When it is on and this
    app starts a cold wake, the gateway also starts restoring the services
    it has measured this app calling soon after it wakes, so their restores
    overlap instead of running one after another. Measurement is per
    gateway and needs at least 20 observed wakes of this app before any
    service is woken ahead. Woken services are billed like any other
    running instance. Requires app read access and completed MFA.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ServiceWakeAheadResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
