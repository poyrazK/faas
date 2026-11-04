from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_task_response import AppTaskResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    run_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/crons/{id}/runs/{run_id}".format(
            id=quote(str(id), safe=""),
            run_id=quote(str(run_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppTaskResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppTaskResponse.from_dict(response.json())

        return response_200

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
) -> Response[AppTaskResponse | Problem]:
    """Get output and execution details for one command-cron run.

     Returns the durable app-task receipt for one run belonging to this
    command cron, including captured stdout/stderr tails, exit status,
    retry count, and failure details. Use this endpoint on demand so
    history list pages remain compact. HTTP cron runs and task ids that
    belong to another cron return 404.

    Args:
        id (str):
        run_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppTaskResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        run_id=run_id,
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
) -> AppTaskResponse | Problem | None:
    """Get output and execution details for one command-cron run.

     Returns the durable app-task receipt for one run belonging to this
    command cron, including captured stdout/stderr tails, exit status,
    retry count, and failure details. Use this endpoint on demand so
    history list pages remain compact. HTTP cron runs and task ids that
    belong to another cron return 404.

    Args:
        id (str):
        run_id (UUID):

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
    ).parsed


async def asyncio_detailed(
    id: str,
    run_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppTaskResponse | Problem]:
    """Get output and execution details for one command-cron run.

     Returns the durable app-task receipt for one run belonging to this
    command cron, including captured stdout/stderr tails, exit status,
    retry count, and failure details. Use this endpoint on demand so
    history list pages remain compact. HTTP cron runs and task ids that
    belong to another cron return 404.

    Args:
        id (str):
        run_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppTaskResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        run_id=run_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    run_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> AppTaskResponse | Problem | None:
    """Get output and execution details for one command-cron run.

     Returns the durable app-task receipt for one run belonging to this
    command cron, including captured stdout/stderr tails, exit status,
    retry count, and failure details. Use this endpoint on demand so
    history list pages remain compact. HTTP cron runs and task ids that
    belong to another cron return 404.

    Args:
        id (str):
        run_id (UUID):

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
        )
    ).parsed
