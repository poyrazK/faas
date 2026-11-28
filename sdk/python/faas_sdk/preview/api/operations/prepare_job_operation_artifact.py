from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_artifact_request import OperationArtifactRequest
from ...models.operation_job_artifact_response import OperationJobArtifactResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: OperationArtifactRequest,
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
        "method": "post",
        "url": "/v1/runtime/job-operations/{id}/artifacts".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationJobArtifactResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationJobArtifactResponse.from_dict(response.json())

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
) -> Response[OperationJobArtifactResponse | Problem]:
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
    body: OperationArtifactRequest,
    x_gregale_operation_job_run_id: UUID,
    x_gregale_operation_job_instance_id: UUID,
    x_gregale_operation_generation: int,
    x_gregale_operation_attempt: int,
) -> Response[OperationJobArtifactResponse | Problem]:
    """Verify and retain a private file for the current Job Operation task.

     Requires the scheduler-injected task capability and matching run, instance, generation, attempt,
    unexpired lease and active owner. Stable report IDs share the progress/result namespace. Changed
    declarations conflict. Preparation verifies source ownership, private scope, size and SHA-256 and
    retains a private platform copy. Same-generation replay does not reread the source. Files remain
    private until host-confirmed success with a typed result or explicit account success recovery. Safe
    retry clears file receipts. Admission remains closed pending native qualification.

    Args:
        id (UUID):
        x_gregale_operation_job_run_id (UUID):
        x_gregale_operation_job_instance_id (UUID):
        x_gregale_operation_generation (int):
        x_gregale_operation_attempt (int):
        body (OperationArtifactRequest): Idempotent attachment of an existing managed object;
            bytes are checked before commit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationJobArtifactResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
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
    body: OperationArtifactRequest,
    x_gregale_operation_job_run_id: UUID,
    x_gregale_operation_job_instance_id: UUID,
    x_gregale_operation_generation: int,
    x_gregale_operation_attempt: int,
) -> OperationJobArtifactResponse | Problem | None:
    """Verify and retain a private file for the current Job Operation task.

     Requires the scheduler-injected task capability and matching run, instance, generation, attempt,
    unexpired lease and active owner. Stable report IDs share the progress/result namespace. Changed
    declarations conflict. Preparation verifies source ownership, private scope, size and SHA-256 and
    retains a private platform copy. Same-generation replay does not reread the source. Files remain
    private until host-confirmed success with a typed result or explicit account success recovery. Safe
    retry clears file receipts. Admission remains closed pending native qualification.

    Args:
        id (UUID):
        x_gregale_operation_job_run_id (UUID):
        x_gregale_operation_job_instance_id (UUID):
        x_gregale_operation_generation (int):
        x_gregale_operation_attempt (int):
        body (OperationArtifactRequest): Idempotent attachment of an existing managed object;
            bytes are checked before commit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationJobArtifactResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        x_gregale_operation_job_run_id=x_gregale_operation_job_run_id,
        x_gregale_operation_job_instance_id=x_gregale_operation_job_instance_id,
        x_gregale_operation_generation=x_gregale_operation_generation,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: Client,
    body: OperationArtifactRequest,
    x_gregale_operation_job_run_id: UUID,
    x_gregale_operation_job_instance_id: UUID,
    x_gregale_operation_generation: int,
    x_gregale_operation_attempt: int,
) -> Response[OperationJobArtifactResponse | Problem]:
    """Verify and retain a private file for the current Job Operation task.

     Requires the scheduler-injected task capability and matching run, instance, generation, attempt,
    unexpired lease and active owner. Stable report IDs share the progress/result namespace. Changed
    declarations conflict. Preparation verifies source ownership, private scope, size and SHA-256 and
    retains a private platform copy. Same-generation replay does not reread the source. Files remain
    private until host-confirmed success with a typed result or explicit account success recovery. Safe
    retry clears file receipts. Admission remains closed pending native qualification.

    Args:
        id (UUID):
        x_gregale_operation_job_run_id (UUID):
        x_gregale_operation_job_instance_id (UUID):
        x_gregale_operation_generation (int):
        x_gregale_operation_attempt (int):
        body (OperationArtifactRequest): Idempotent attachment of an existing managed object;
            bytes are checked before commit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationJobArtifactResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
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
    body: OperationArtifactRequest,
    x_gregale_operation_job_run_id: UUID,
    x_gregale_operation_job_instance_id: UUID,
    x_gregale_operation_generation: int,
    x_gregale_operation_attempt: int,
) -> OperationJobArtifactResponse | Problem | None:
    """Verify and retain a private file for the current Job Operation task.

     Requires the scheduler-injected task capability and matching run, instance, generation, attempt,
    unexpired lease and active owner. Stable report IDs share the progress/result namespace. Changed
    declarations conflict. Preparation verifies source ownership, private scope, size and SHA-256 and
    retains a private platform copy. Same-generation replay does not reread the source. Files remain
    private until host-confirmed success with a typed result or explicit account success recovery. Safe
    retry clears file receipts. Admission remains closed pending native qualification.

    Args:
        id (UUID):
        x_gregale_operation_job_run_id (UUID):
        x_gregale_operation_job_instance_id (UUID):
        x_gregale_operation_generation (int):
        x_gregale_operation_attempt (int):
        body (OperationArtifactRequest): Idempotent attachment of an existing managed object;
            bytes are checked before commit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationJobArtifactResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            x_gregale_operation_job_run_id=x_gregale_operation_job_run_id,
            x_gregale_operation_job_instance_id=x_gregale_operation_job_instance_id,
            x_gregale_operation_generation=x_gregale_operation_generation,
            x_gregale_operation_attempt=x_gregale_operation_attempt,
        )
    ).parsed
