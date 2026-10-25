from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_job_control_response import OperationJobControlResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    x_gregale_operation_job_run_id: UUID,
    x_gregale_operation_job_instance_id: UUID,
    x_gregale_operation_generation: int,
    x_gregale_operation_attempt: int,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Gregale-Operation-Job-Run-Id"] = str(x_gregale_operation_job_run_id)

    headers["X-Gregale-Operation-Job-Instance-Id"] = str(x_gregale_operation_job_instance_id)

    headers["X-Gregale-Operation-Generation"] = str(x_gregale_operation_generation)

    headers["X-Gregale-Operation-Attempt"] = str(x_gregale_operation_attempt)

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/runtime/job-operations/{id}/control".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationJobControlResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationJobControlResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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
) -> Response[OperationJobControlResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: Client,
    x_gregale_operation_job_run_id: UUID,
    x_gregale_operation_job_instance_id: UUID,
    x_gregale_operation_generation: int,
    x_gregale_operation_attempt: int,
) -> Response[OperationJobControlResponse | Problem]:
    """Observe the current single-task Job Operation lease.

     Observe cancellation, the current lease and the fixed deadline using the scheduler-injected native
    task proof. Reads grant no renewal, result publication or retry authority. Account credentials and
    other runtime proof kinds cannot substitute. Admission remains closed pending native qualification.

    Args:
        id (UUID):
        x_gregale_operation_job_run_id (UUID):
        x_gregale_operation_job_instance_id (UUID):
        x_gregale_operation_generation (int):
        x_gregale_operation_attempt (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationJobControlResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        x_gregale_operation_job_run_id=x_gregale_operation_job_run_id,
        x_gregale_operation_job_instance_id=x_gregale_operation_job_instance_id,
        x_gregale_operation_generation=x_gregale_operation_generation,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: Client,
    x_gregale_operation_job_run_id: UUID,
    x_gregale_operation_job_instance_id: UUID,
    x_gregale_operation_generation: int,
    x_gregale_operation_attempt: int,
) -> OperationJobControlResponse | Problem | None:
    """Observe the current single-task Job Operation lease.

     Observe cancellation, the current lease and the fixed deadline using the scheduler-injected native
    task proof. Reads grant no renewal, result publication or retry authority. Account credentials and
    other runtime proof kinds cannot substitute. Admission remains closed pending native qualification.

    Args:
        id (UUID):
        x_gregale_operation_job_run_id (UUID):
        x_gregale_operation_job_instance_id (UUID):
        x_gregale_operation_generation (int):
        x_gregale_operation_attempt (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationJobControlResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        x_gregale_operation_job_run_id=x_gregale_operation_job_run_id,
        x_gregale_operation_job_instance_id=x_gregale_operation_job_instance_id,
        x_gregale_operation_generation=x_gregale_operation_generation,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: Client,
    x_gregale_operation_job_run_id: UUID,
    x_gregale_operation_job_instance_id: UUID,
    x_gregale_operation_generation: int,
    x_gregale_operation_attempt: int,
) -> Response[OperationJobControlResponse | Problem]:
    """Observe the current single-task Job Operation lease.

     Observe cancellation, the current lease and the fixed deadline using the scheduler-injected native
    task proof. Reads grant no renewal, result publication or retry authority. Account credentials and
    other runtime proof kinds cannot substitute. Admission remains closed pending native qualification.

    Args:
        id (UUID):
        x_gregale_operation_job_run_id (UUID):
        x_gregale_operation_job_instance_id (UUID):
        x_gregale_operation_generation (int):
        x_gregale_operation_attempt (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationJobControlResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        x_gregale_operation_job_run_id=x_gregale_operation_job_run_id,
        x_gregale_operation_job_instance_id=x_gregale_operation_job_instance_id,
        x_gregale_operation_generation=x_gregale_operation_generation,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: Client,
    x_gregale_operation_job_run_id: UUID,
    x_gregale_operation_job_instance_id: UUID,
    x_gregale_operation_generation: int,
    x_gregale_operation_attempt: int,
) -> OperationJobControlResponse | Problem | None:
    """Observe the current single-task Job Operation lease.

     Observe cancellation, the current lease and the fixed deadline using the scheduler-injected native
    task proof. Reads grant no renewal, result publication or retry authority. Account credentials and
    other runtime proof kinds cannot substitute. Admission remains closed pending native qualification.

    Args:
        id (UUID):
        x_gregale_operation_job_run_id (UUID):
        x_gregale_operation_job_instance_id (UUID):
        x_gregale_operation_generation (int):
        x_gregale_operation_attempt (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationJobControlResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            x_gregale_operation_job_run_id=x_gregale_operation_job_run_id,
            x_gregale_operation_job_instance_id=x_gregale_operation_job_instance_id,
            x_gregale_operation_generation=x_gregale_operation_generation,
            x_gregale_operation_attempt=x_gregale_operation_attempt,
        )
    ).parsed
