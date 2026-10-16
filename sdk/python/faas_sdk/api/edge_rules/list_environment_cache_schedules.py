import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.environment_cache_schedule_list import EnvironmentCacheScheduleList
from ...models.list_environment_cache_schedules_status import (
    ListEnvironmentCacheSchedulesStatus,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    status: ListEnvironmentCacheSchedulesStatus | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 20,
    environment: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_status: str | Unset = UNSET
    if not isinstance(status, Unset):
        json_status = status

    params["status"] = json_status

    json_from_: str | Unset = UNSET
    if not isinstance(from_, Unset):
        json_from_ = from_.isoformat()
    params["from"] = json_from_

    json_until: str | Unset = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params["after"] = after

    params["limit"] = limit

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/cache/schedules".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EnvironmentCacheScheduleList | Problem | None:
    if response.status_code == 200:
        response_200 = EnvironmentCacheScheduleList.from_dict(response.json())

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
) -> Response[EnvironmentCacheScheduleList | Problem]:
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
    status: ListEnvironmentCacheSchedulesStatus | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 20,
    environment: str,
) -> Response[EnvironmentCacheScheduleList | Problem]:
    """Discover named environment cache schedules with bounded cursor pages.

     Requires app read access and MFA. Returns schedules for the immutable
    environment in ascending execute_at, schedule_id order, including linked
    replacements. Optional status, inclusive from and exclusive until filters
    are bound into the opaque continuation cursor along with immutable target
    IDs. Default page size 20, maximum 50. Pages are live, not a frozen snapshot;
    status changes and new replacements may require restarting from page one.
    Schedule execution receipts are distinct from gateway convergence evidence.

    Args:
        slug (str):
        status (ListEnvironmentCacheSchedulesStatus | Unset):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 20.
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCacheScheduleList | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        status=status,
        from_=from_,
        until=until,
        after=after,
        limit=limit,
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
    status: ListEnvironmentCacheSchedulesStatus | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 20,
    environment: str,
) -> EnvironmentCacheScheduleList | Problem | None:
    """Discover named environment cache schedules with bounded cursor pages.

     Requires app read access and MFA. Returns schedules for the immutable
    environment in ascending execute_at, schedule_id order, including linked
    replacements. Optional status, inclusive from and exclusive until filters
    are bound into the opaque continuation cursor along with immutable target
    IDs. Default page size 20, maximum 50. Pages are live, not a frozen snapshot;
    status changes and new replacements may require restarting from page one.
    Schedule execution receipts are distinct from gateway convergence evidence.

    Args:
        slug (str):
        status (ListEnvironmentCacheSchedulesStatus | Unset):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 20.
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCacheScheduleList | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        status=status,
        from_=from_,
        until=until,
        after=after,
        limit=limit,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    status: ListEnvironmentCacheSchedulesStatus | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 20,
    environment: str,
) -> Response[EnvironmentCacheScheduleList | Problem]:
    """Discover named environment cache schedules with bounded cursor pages.

     Requires app read access and MFA. Returns schedules for the immutable
    environment in ascending execute_at, schedule_id order, including linked
    replacements. Optional status, inclusive from and exclusive until filters
    are bound into the opaque continuation cursor along with immutable target
    IDs. Default page size 20, maximum 50. Pages are live, not a frozen snapshot;
    status changes and new replacements may require restarting from page one.
    Schedule execution receipts are distinct from gateway convergence evidence.

    Args:
        slug (str):
        status (ListEnvironmentCacheSchedulesStatus | Unset):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 20.
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCacheScheduleList | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        status=status,
        from_=from_,
        until=until,
        after=after,
        limit=limit,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    status: ListEnvironmentCacheSchedulesStatus | Unset = UNSET,
    from_: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 20,
    environment: str,
) -> EnvironmentCacheScheduleList | Problem | None:
    """Discover named environment cache schedules with bounded cursor pages.

     Requires app read access and MFA. Returns schedules for the immutable
    environment in ascending execute_at, schedule_id order, including linked
    replacements. Optional status, inclusive from and exclusive until filters
    are bound into the opaque continuation cursor along with immutable target
    IDs. Default page size 20, maximum 50. Pages are live, not a frozen snapshot;
    status changes and new replacements may require restarting from page one.
    Schedule execution receipts are distinct from gateway convergence evidence.

    Args:
        slug (str):
        status (ListEnvironmentCacheSchedulesStatus | Unset):
        from_ (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 20.
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCacheScheduleList | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            status=status,
            from_=from_,
            until=until,
            after=after,
            limit=limit,
            environment=environment,
        )
    ).parsed
