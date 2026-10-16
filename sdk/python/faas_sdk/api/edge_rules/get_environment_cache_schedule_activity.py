from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.environment_cache_schedule_activity import EnvironmentCacheScheduleActivity
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    after: str | Unset = UNSET,
    limit: int | Unset = 20,
    environment: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["after"] = after

    params["limit"] = limit

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/cache/schedules/activity".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EnvironmentCacheScheduleActivity | Problem | None:
    if response.status_code == 200:
        response_200 = EnvironmentCacheScheduleActivity.from_dict(response.json())

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

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EnvironmentCacheScheduleActivity | Problem]:
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
    after: str | Unset = UNSET,
    limit: int | Unset = 20,
    environment: str,
) -> Response[EnvironmentCacheScheduleActivity | Problem]:
    """Catch up on durable environment cache schedule transitions.

     Requires app read access and MFA. Records accepted, rescheduled, cancelled,
    executed and failed transitions in the same transaction as the schedule
    change. A per-app/environment transactional counter serializes sequence
    allocation through commit, so cursors cannot skip a late transaction.
    Rescheduling emits the cancelled original with its successor ID followed
    by acceptance of the successor linked to its predecessor. Identical retries
    and stale worker completions emit no additional events. Claim/lease retries
    are not outcomes and emit no events. Execution includes only the policy
    revision or purge generation receipt; gateway convergence is separate.
    Cursors bind account and immutable app/project/environment IDs. Rejects
    malformed, foreign and ahead-of-stream checkpoints. Entries are ascending
    and contiguous; cursor always identifies the last returned sequence, or
    the supplied checkpoint for an empty page. Persist it even at the tail
    and supply it again after reconnecting. Has_more means another committed
    event was visible in the same snapshot. Events retain compact metadata,
    timestamps and stable failure codes, without rule bodies or paths. They
    are retained for 30 days plus bounded cleanup lag. An expired checkpoint
    returns 410 cursor_expired with scoped activity_recovery metadata. Save
    restart_cursor before listing a current schedule baseline, then consume
    events after it and reconcile overlapping schedule identities. Removed
    historical outcomes cannot be reconstructed. Recording begins with upgraded apid writers;
    no historical events are fabricated. Use schedule listing for the initial
    baseline and enable catch-up only after all apid writers are upgraded.

    Args:
        slug (str):
        after (str | Unset):
        limit (int | Unset):  Default: 20.
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCacheScheduleActivity | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
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
    after: str | Unset = UNSET,
    limit: int | Unset = 20,
    environment: str,
) -> EnvironmentCacheScheduleActivity | Problem | None:
    """Catch up on durable environment cache schedule transitions.

     Requires app read access and MFA. Records accepted, rescheduled, cancelled,
    executed and failed transitions in the same transaction as the schedule
    change. A per-app/environment transactional counter serializes sequence
    allocation through commit, so cursors cannot skip a late transaction.
    Rescheduling emits the cancelled original with its successor ID followed
    by acceptance of the successor linked to its predecessor. Identical retries
    and stale worker completions emit no additional events. Claim/lease retries
    are not outcomes and emit no events. Execution includes only the policy
    revision or purge generation receipt; gateway convergence is separate.
    Cursors bind account and immutable app/project/environment IDs. Rejects
    malformed, foreign and ahead-of-stream checkpoints. Entries are ascending
    and contiguous; cursor always identifies the last returned sequence, or
    the supplied checkpoint for an empty page. Persist it even at the tail
    and supply it again after reconnecting. Has_more means another committed
    event was visible in the same snapshot. Events retain compact metadata,
    timestamps and stable failure codes, without rule bodies or paths. They
    are retained for 30 days plus bounded cleanup lag. An expired checkpoint
    returns 410 cursor_expired with scoped activity_recovery metadata. Save
    restart_cursor before listing a current schedule baseline, then consume
    events after it and reconcile overlapping schedule identities. Removed
    historical outcomes cannot be reconstructed. Recording begins with upgraded apid writers;
    no historical events are fabricated. Use schedule listing for the initial
    baseline and enable catch-up only after all apid writers are upgraded.

    Args:
        slug (str):
        after (str | Unset):
        limit (int | Unset):  Default: 20.
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCacheScheduleActivity | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        after=after,
        limit=limit,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    after: str | Unset = UNSET,
    limit: int | Unset = 20,
    environment: str,
) -> Response[EnvironmentCacheScheduleActivity | Problem]:
    """Catch up on durable environment cache schedule transitions.

     Requires app read access and MFA. Records accepted, rescheduled, cancelled,
    executed and failed transitions in the same transaction as the schedule
    change. A per-app/environment transactional counter serializes sequence
    allocation through commit, so cursors cannot skip a late transaction.
    Rescheduling emits the cancelled original with its successor ID followed
    by acceptance of the successor linked to its predecessor. Identical retries
    and stale worker completions emit no additional events. Claim/lease retries
    are not outcomes and emit no events. Execution includes only the policy
    revision or purge generation receipt; gateway convergence is separate.
    Cursors bind account and immutable app/project/environment IDs. Rejects
    malformed, foreign and ahead-of-stream checkpoints. Entries are ascending
    and contiguous; cursor always identifies the last returned sequence, or
    the supplied checkpoint for an empty page. Persist it even at the tail
    and supply it again after reconnecting. Has_more means another committed
    event was visible in the same snapshot. Events retain compact metadata,
    timestamps and stable failure codes, without rule bodies or paths. They
    are retained for 30 days plus bounded cleanup lag. An expired checkpoint
    returns 410 cursor_expired with scoped activity_recovery metadata. Save
    restart_cursor before listing a current schedule baseline, then consume
    events after it and reconcile overlapping schedule identities. Removed
    historical outcomes cannot be reconstructed. Recording begins with upgraded apid writers;
    no historical events are fabricated. Use schedule listing for the initial
    baseline and enable catch-up only after all apid writers are upgraded.

    Args:
        slug (str):
        after (str | Unset):
        limit (int | Unset):  Default: 20.
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EnvironmentCacheScheduleActivity | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
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
    after: str | Unset = UNSET,
    limit: int | Unset = 20,
    environment: str,
) -> EnvironmentCacheScheduleActivity | Problem | None:
    """Catch up on durable environment cache schedule transitions.

     Requires app read access and MFA. Records accepted, rescheduled, cancelled,
    executed and failed transitions in the same transaction as the schedule
    change. A per-app/environment transactional counter serializes sequence
    allocation through commit, so cursors cannot skip a late transaction.
    Rescheduling emits the cancelled original with its successor ID followed
    by acceptance of the successor linked to its predecessor. Identical retries
    and stale worker completions emit no additional events. Claim/lease retries
    are not outcomes and emit no events. Execution includes only the policy
    revision or purge generation receipt; gateway convergence is separate.
    Cursors bind account and immutable app/project/environment IDs. Rejects
    malformed, foreign and ahead-of-stream checkpoints. Entries are ascending
    and contiguous; cursor always identifies the last returned sequence, or
    the supplied checkpoint for an empty page. Persist it even at the tail
    and supply it again after reconnecting. Has_more means another committed
    event was visible in the same snapshot. Events retain compact metadata,
    timestamps and stable failure codes, without rule bodies or paths. They
    are retained for 30 days plus bounded cleanup lag. An expired checkpoint
    returns 410 cursor_expired with scoped activity_recovery metadata. Save
    restart_cursor before listing a current schedule baseline, then consume
    events after it and reconcile overlapping schedule identities. Removed
    historical outcomes cannot be reconstructed. Recording begins with upgraded apid writers;
    no historical events are fabricated. Use schedule listing for the initial
    baseline and enable catch-up only after all apid writers are upgraded.

    Args:
        slug (str):
        after (str | Unset):
        limit (int | Unset):  Default: 20.
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EnvironmentCacheScheduleActivity | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            after=after,
            limit=limit,
            environment=environment,
        )
    ).parsed
