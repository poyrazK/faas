from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.job_run_cancelled_response import JobRunCancelledResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    name: str,
    id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/jobs/{name}/runs/{id}/cancel".format(
            name=quote(str(name), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> JobRunCancelledResponse | Problem | None:
    if response.status_code == 200:
        response_200 = JobRunCancelledResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[JobRunCancelledResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    name: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[JobRunCancelledResponse | Problem]:
    """Cancel a run.

     Transitions every non-terminal task of the run to
    status='cancelled'. For claimed (running) tasks,
    schedd SIGTERMs the guest; the supervisor catches
    SIGTERM, writes job_exit{error_class='cancelled',
    exit_code=143}, then poweroff.

    Args:
        name (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[JobRunCancelledResponse | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    name: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> JobRunCancelledResponse | Problem | None:
    """Cancel a run.

     Transitions every non-terminal task of the run to
    status='cancelled'. For claimed (running) tasks,
    schedd SIGTERMs the guest; the supervisor catches
    SIGTERM, writes job_exit{error_class='cancelled',
    exit_code=143}, then poweroff.

    Args:
        name (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        JobRunCancelledResponse | Problem
    """

    return sync_detailed(
        name=name,
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    name: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[JobRunCancelledResponse | Problem]:
    """Cancel a run.

     Transitions every non-terminal task of the run to
    status='cancelled'. For claimed (running) tasks,
    schedd SIGTERMs the guest; the supervisor catches
    SIGTERM, writes job_exit{error_class='cancelled',
    exit_code=143}, then poweroff.

    Args:
        name (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[JobRunCancelledResponse | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    name: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> JobRunCancelledResponse | Problem | None:
    """Cancel a run.

     Transitions every non-terminal task of the run to
    status='cancelled'. For claimed (running) tasks,
    schedd SIGTERMs the guest; the supervisor catches
    SIGTERM, writes job_exit{error_class='cancelled',
    exit_code=143}, then poweroff.

    Args:
        name (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        JobRunCancelledResponse | Problem
    """

    return (
        await asyncio_detailed(
            name=name,
            id=id,
            client=client,
        )
    ).parsed
