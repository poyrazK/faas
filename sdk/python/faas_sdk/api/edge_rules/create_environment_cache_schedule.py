from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_environment_cache_schedule_request import CreateEnvironmentCacheScheduleRequest
from ...models.environment_cache_schedule import EnvironmentCacheSchedule
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateEnvironmentCacheScheduleRequest,
    environment: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/cache/schedules".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EnvironmentCacheSchedule | Problem | None:
    if response.status_code == 200:
        response_200 = EnvironmentCacheSchedule.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EnvironmentCacheSchedule | Problem]:
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
    body: CreateEnvironmentCacheScheduleRequest,
    environment: str,
) -> Response[EnvironmentCacheSchedule | Problem]:
    """Accept one future environment cache action.

     Requires app write access and MFA. Delegates exactly one policy replacement,
    inheritance restore or purge to apid at or after execute_at, within 30 days
    of acceptance. At most 100 scheduled/executing actions per app/environment.
    Schedule UUID doubles as the action request UUID; immutable IDs and complete
    action are required. Policy pins expected_revision both at acceptance and
    execution; copy_from and restore_revision are unsupported. Purge requires
    explicit path, including empty string for the whole environment host.
    Identical retries recover the same schedule even after execution time or
    terminal completion. Schedule acceptance is not action execution or gateway
    convergence. Durable delegation survives client disconnect and token expiry;
    cancel explicitly before worker claim. Delayed actions execute after outage
    recovery; execution time is a lower bound, not a real-time deadline.

    Args:
        slug (str):
        environment (str):
        body (CreateEnvironmentCacheScheduleRequest): One durable environment cache action;
            exactly one policy or purge is required, with request_id equal to schedule_id.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCacheSchedule | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateEnvironmentCacheScheduleRequest,
    environment: str,
) -> EnvironmentCacheSchedule | Problem | None:
    """Accept one future environment cache action.

     Requires app write access and MFA. Delegates exactly one policy replacement,
    inheritance restore or purge to apid at or after execute_at, within 30 days
    of acceptance. At most 100 scheduled/executing actions per app/environment.
    Schedule UUID doubles as the action request UUID; immutable IDs and complete
    action are required. Policy pins expected_revision both at acceptance and
    execution; copy_from and restore_revision are unsupported. Purge requires
    explicit path, including empty string for the whole environment host.
    Identical retries recover the same schedule even after execution time or
    terminal completion. Schedule acceptance is not action execution or gateway
    convergence. Durable delegation survives client disconnect and token expiry;
    cancel explicitly before worker claim. Delayed actions execute after outage
    recovery; execution time is a lower bound, not a real-time deadline.

    Args:
        slug (str):
        environment (str):
        body (CreateEnvironmentCacheScheduleRequest): One durable environment cache action;
            exactly one policy or purge is required, with request_id equal to schedule_id.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCacheSchedule | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateEnvironmentCacheScheduleRequest,
    environment: str,
) -> Response[EnvironmentCacheSchedule | Problem]:
    """Accept one future environment cache action.

     Requires app write access and MFA. Delegates exactly one policy replacement,
    inheritance restore or purge to apid at or after execute_at, within 30 days
    of acceptance. At most 100 scheduled/executing actions per app/environment.
    Schedule UUID doubles as the action request UUID; immutable IDs and complete
    action are required. Policy pins expected_revision both at acceptance and
    execution; copy_from and restore_revision are unsupported. Purge requires
    explicit path, including empty string for the whole environment host.
    Identical retries recover the same schedule even after execution time or
    terminal completion. Schedule acceptance is not action execution or gateway
    convergence. Durable delegation survives client disconnect and token expiry;
    cancel explicitly before worker claim. Delayed actions execute after outage
    recovery; execution time is a lower bound, not a real-time deadline.

    Args:
        slug (str):
        environment (str):
        body (CreateEnvironmentCacheScheduleRequest): One durable environment cache action;
            exactly one policy or purge is required, with request_id equal to schedule_id.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCacheSchedule | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateEnvironmentCacheScheduleRequest,
    environment: str,
) -> EnvironmentCacheSchedule | Problem | None:
    """Accept one future environment cache action.

     Requires app write access and MFA. Delegates exactly one policy replacement,
    inheritance restore or purge to apid at or after execute_at, within 30 days
    of acceptance. At most 100 scheduled/executing actions per app/environment.
    Schedule UUID doubles as the action request UUID; immutable IDs and complete
    action are required. Policy pins expected_revision both at acceptance and
    execution; copy_from and restore_revision are unsupported. Purge requires
    explicit path, including empty string for the whole environment host.
    Identical retries recover the same schedule even after execution time or
    terminal completion. Schedule acceptance is not action execution or gateway
    convergence. Durable delegation survives client disconnect and token expiry;
    cancel explicitly before worker claim. Delayed actions execute after outage
    recovery; execution time is a lower bound, not a real-time deadline.

    Args:
        slug (str):
        environment (str):
        body (CreateEnvironmentCacheScheduleRequest): One durable environment cache action;
            exactly one policy or purge is required, with request_id equal to schedule_id.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCacheSchedule | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            environment=environment,
        )
    ).parsed
