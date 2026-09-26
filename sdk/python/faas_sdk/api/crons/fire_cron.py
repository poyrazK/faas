from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.fire_cron_response import FireCronResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/crons/{id}/run".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FireCronResponse | Problem | None:
    if response.status_code == 202:
        response_202 = FireCronResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

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
) -> Response[FireCronResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[FireCronResponse | Problem]:
    """Manually fire a cron now (bypasses the schedule boundary).

     Inserts a pending row into
    `cron_fire_now_requests` and emits `db.NotifyCronRunNow`;
    schedd claims the row on the next LISTEN delivery. HTTP crons
    dispatch through `RunCronNow`; command crons enqueue a task
    pinned to the current live deployment. Poll the request to
    obtain its invocation id or command task id.

    Idempotent: a replay with the same Idempotency-Key returns
    the stored 202 without enqueuing a second fire.

    Scoped to `deploy:write` (or `admin`); no new `cron:write`
    scope is added (ADR-090 §Sub-decisions 1). The fire does
    NOT shift `last_fired_at` — the next scheduled boundary is
    unaffected. For a command cron, its saved command, timeout,
    output limit, retry policy, skip_if_running behavior, and
    current live deployment are used; no ad-hoc command may be
    supplied here.

    Args:
        id (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FireCronResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> FireCronResponse | Problem | None:
    """Manually fire a cron now (bypasses the schedule boundary).

     Inserts a pending row into
    `cron_fire_now_requests` and emits `db.NotifyCronRunNow`;
    schedd claims the row on the next LISTEN delivery. HTTP crons
    dispatch through `RunCronNow`; command crons enqueue a task
    pinned to the current live deployment. Poll the request to
    obtain its invocation id or command task id.

    Idempotent: a replay with the same Idempotency-Key returns
    the stored 202 without enqueuing a second fire.

    Scoped to `deploy:write` (or `admin`); no new `cron:write`
    scope is added (ADR-090 §Sub-decisions 1). The fire does
    NOT shift `last_fired_at` — the next scheduled boundary is
    unaffected. For a command cron, its saved command, timeout,
    output limit, retry policy, skip_if_running behavior, and
    current live deployment are used; no ad-hoc command may be
    supplied here.

    Args:
        id (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FireCronResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[FireCronResponse | Problem]:
    """Manually fire a cron now (bypasses the schedule boundary).

     Inserts a pending row into
    `cron_fire_now_requests` and emits `db.NotifyCronRunNow`;
    schedd claims the row on the next LISTEN delivery. HTTP crons
    dispatch through `RunCronNow`; command crons enqueue a task
    pinned to the current live deployment. Poll the request to
    obtain its invocation id or command task id.

    Idempotent: a replay with the same Idempotency-Key returns
    the stored 202 without enqueuing a second fire.

    Scoped to `deploy:write` (or `admin`); no new `cron:write`
    scope is added (ADR-090 §Sub-decisions 1). The fire does
    NOT shift `last_fired_at` — the next scheduled boundary is
    unaffected. For a command cron, its saved command, timeout,
    output limit, retry policy, skip_if_running behavior, and
    current live deployment are used; no ad-hoc command may be
    supplied here.

    Args:
        id (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FireCronResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> FireCronResponse | Problem | None:
    """Manually fire a cron now (bypasses the schedule boundary).

     Inserts a pending row into
    `cron_fire_now_requests` and emits `db.NotifyCronRunNow`;
    schedd claims the row on the next LISTEN delivery. HTTP crons
    dispatch through `RunCronNow`; command crons enqueue a task
    pinned to the current live deployment. Poll the request to
    obtain its invocation id or command task id.

    Idempotent: a replay with the same Idempotency-Key returns
    the stored 202 without enqueuing a second fire.

    Scoped to `deploy:write` (or `admin`); no new `cron:write`
    scope is added (ADR-090 §Sub-decisions 1). The fire does
    NOT shift `last_fired_at` — the next scheduled boundary is
    unaffected. For a command cron, its saved command, timeout,
    output limit, retry policy, skip_if_running behavior, and
    current live deployment are used; no ad-hoc command may be
    supplied here.

    Args:
        id (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FireCronResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            idempotency_key=idempotency_key,
        )
    ).parsed
