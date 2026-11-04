from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_task_response import AppTaskResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    run_id: UUID,
    *,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/crons/{id}/runs/{run_id}/cancel".format(
            id=quote(str(id), safe=""),
            run_id=quote(str(run_id), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppTaskResponse | Problem | None:
    if response.status_code == 202:
        response_202 = AppTaskResponse.from_dict(response.json())

        return response_202

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppTaskResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    run_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[AppTaskResponse | Problem]:
    """Request cancellation of one command-cron run.

     Requests cancellation of one queued or active command-cron run and
    returns its current durable app-task receipt. A queued run becomes
    cancelled immediately; an active run records `cancel_requested_at`
    and is stopped by its worker. Repeating the request is safe. Runs
    belonging to another cron, HTTP cron runs, and runs owned by another
    account return the same 404.

    Scoped to `deploy:write` (or `admin`) and subject to the app-task API
    capability gate. An optional `Idempotency-Key` replays the stored
    response for the account/key pair.

    Args:
        id (str):
        run_id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppTaskResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        run_id=run_id,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    run_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> AppTaskResponse | Problem | None:
    """Request cancellation of one command-cron run.

     Requests cancellation of one queued or active command-cron run and
    returns its current durable app-task receipt. A queued run becomes
    cancelled immediately; an active run records `cancel_requested_at`
    and is stopped by its worker. Repeating the request is safe. Runs
    belonging to another cron, HTTP cron runs, and runs owned by another
    account return the same 404.

    Scoped to `deploy:write` (or `admin`) and subject to the app-task API
    capability gate. An optional `Idempotency-Key` replays the stored
    response for the account/key pair.

    Args:
        id (str):
        run_id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppTaskResponse | Problem
    """

    return sync_detailed(
        id=id,
        run_id=run_id,
        client=client,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: str,
    run_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[AppTaskResponse | Problem]:
    """Request cancellation of one command-cron run.

     Requests cancellation of one queued or active command-cron run and
    returns its current durable app-task receipt. A queued run becomes
    cancelled immediately; an active run records `cancel_requested_at`
    and is stopped by its worker. Repeating the request is safe. Runs
    belonging to another cron, HTTP cron runs, and runs owned by another
    account return the same 404.

    Scoped to `deploy:write` (or `admin`) and subject to the app-task API
    capability gate. An optional `Idempotency-Key` replays the stored
    response for the account/key pair.

    Args:
        id (str):
        run_id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppTaskResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        run_id=run_id,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    run_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> AppTaskResponse | Problem | None:
    """Request cancellation of one command-cron run.

     Requests cancellation of one queued or active command-cron run and
    returns its current durable app-task receipt. A queued run becomes
    cancelled immediately; an active run records `cancel_requested_at`
    and is stopped by its worker. Repeating the request is safe. Runs
    belonging to another cron, HTTP cron runs, and runs owned by another
    account return the same 404.

    Scoped to `deploy:write` (or `admin`) and subject to the app-task API
    capability gate. An optional `Idempotency-Key` replays the stored
    response for the account/key pair.

    Args:
        id (str):
        run_id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppTaskResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            run_id=run_id,
            client=client,
            idempotency_key=idempotency_key,
        )
    ).parsed
