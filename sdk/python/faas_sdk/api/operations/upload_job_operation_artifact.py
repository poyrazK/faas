from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_job_artifact_response import OperationJobArtifactResponse
from ...models.problem import Problem
from ...types import UNSET, File, Response


def _get_kwargs(
    id: UUID,
    *,
    body: File,
    report_id: str,
    name: str,
    size_bytes: int,
    sha256: str,
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

    params: dict[str, Any] = {}

    params["report_id"] = report_id

    params["name"] = name

    params["size_bytes"] = size_bytes

    params["sha256"] = sha256

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/runtime/job-operations/{id}/artifact-uploads".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    _kwargs["content"] = body.payload
    headers["Content-Type"] = "application/octet-stream"

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

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
    body: File,
    report_id: str,
    name: str,
    size_bytes: int,
    sha256: str,
    x_gregale_operation_job_run_id: UUID,
    x_gregale_operation_job_instance_id: UUID,
    x_gregale_operation_generation: int,
    x_gregale_operation_attempt: int,
) -> Response[OperationJobArtifactResponse | Problem]:
    """Upload direct private file bytes with current native Job authority.

     Stable report ID, name, exact size and SHA-256 bind a private upload receipt. Requires the current
    scheduler task proof and active owner. No source URI, bucket, provider credential or storage key is
    accepted. Exact bytes are verified under existing artifact quotas and bounded transfer budgets. A
    fresh staging object is reserved for each copy; concurrent copies converge and losing objects are
    cleaned up. Committed replay can return without reading the body. Files publish only with confirmed
    task success and typed output or explicit account success recovery. Production admission remains
    closed pending native qualification.

    Args:
        id (UUID):
        report_id (str):
        name (str):
        size_bytes (int):
        sha256 (str):
        x_gregale_operation_job_run_id (UUID):
        x_gregale_operation_job_instance_id (UUID):
        x_gregale_operation_generation (int):
        x_gregale_operation_attempt (int):
        body (File):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationJobArtifactResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        report_id=report_id,
        name=name,
        size_bytes=size_bytes,
        sha256=sha256,
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
    body: File,
    report_id: str,
    name: str,
    size_bytes: int,
    sha256: str,
    x_gregale_operation_job_run_id: UUID,
    x_gregale_operation_job_instance_id: UUID,
    x_gregale_operation_generation: int,
    x_gregale_operation_attempt: int,
) -> OperationJobArtifactResponse | Problem | None:
    """Upload direct private file bytes with current native Job authority.

     Stable report ID, name, exact size and SHA-256 bind a private upload receipt. Requires the current
    scheduler task proof and active owner. No source URI, bucket, provider credential or storage key is
    accepted. Exact bytes are verified under existing artifact quotas and bounded transfer budgets. A
    fresh staging object is reserved for each copy; concurrent copies converge and losing objects are
    cleaned up. Committed replay can return without reading the body. Files publish only with confirmed
    task success and typed output or explicit account success recovery. Production admission remains
    closed pending native qualification.

    Args:
        id (UUID):
        report_id (str):
        name (str):
        size_bytes (int):
        sha256 (str):
        x_gregale_operation_job_run_id (UUID):
        x_gregale_operation_job_instance_id (UUID):
        x_gregale_operation_generation (int):
        x_gregale_operation_attempt (int):
        body (File):

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
        report_id=report_id,
        name=name,
        size_bytes=size_bytes,
        sha256=sha256,
        x_gregale_operation_job_run_id=x_gregale_operation_job_run_id,
        x_gregale_operation_job_instance_id=x_gregale_operation_job_instance_id,
        x_gregale_operation_generation=x_gregale_operation_generation,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: Client,
    body: File,
    report_id: str,
    name: str,
    size_bytes: int,
    sha256: str,
    x_gregale_operation_job_run_id: UUID,
    x_gregale_operation_job_instance_id: UUID,
    x_gregale_operation_generation: int,
    x_gregale_operation_attempt: int,
) -> Response[OperationJobArtifactResponse | Problem]:
    """Upload direct private file bytes with current native Job authority.

     Stable report ID, name, exact size and SHA-256 bind a private upload receipt. Requires the current
    scheduler task proof and active owner. No source URI, bucket, provider credential or storage key is
    accepted. Exact bytes are verified under existing artifact quotas and bounded transfer budgets. A
    fresh staging object is reserved for each copy; concurrent copies converge and losing objects are
    cleaned up. Committed replay can return without reading the body. Files publish only with confirmed
    task success and typed output or explicit account success recovery. Production admission remains
    closed pending native qualification.

    Args:
        id (UUID):
        report_id (str):
        name (str):
        size_bytes (int):
        sha256 (str):
        x_gregale_operation_job_run_id (UUID):
        x_gregale_operation_job_instance_id (UUID):
        x_gregale_operation_generation (int):
        x_gregale_operation_attempt (int):
        body (File):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationJobArtifactResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        report_id=report_id,
        name=name,
        size_bytes=size_bytes,
        sha256=sha256,
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
    body: File,
    report_id: str,
    name: str,
    size_bytes: int,
    sha256: str,
    x_gregale_operation_job_run_id: UUID,
    x_gregale_operation_job_instance_id: UUID,
    x_gregale_operation_generation: int,
    x_gregale_operation_attempt: int,
) -> OperationJobArtifactResponse | Problem | None:
    """Upload direct private file bytes with current native Job authority.

     Stable report ID, name, exact size and SHA-256 bind a private upload receipt. Requires the current
    scheduler task proof and active owner. No source URI, bucket, provider credential or storage key is
    accepted. Exact bytes are verified under existing artifact quotas and bounded transfer budgets. A
    fresh staging object is reserved for each copy; concurrent copies converge and losing objects are
    cleaned up. Committed replay can return without reading the body. Files publish only with confirmed
    task success and typed output or explicit account success recovery. Production admission remains
    closed pending native qualification.

    Args:
        id (UUID):
        report_id (str):
        name (str):
        size_bytes (int):
        sha256 (str):
        x_gregale_operation_job_run_id (UUID):
        x_gregale_operation_job_instance_id (UUID):
        x_gregale_operation_generation (int):
        x_gregale_operation_attempt (int):
        body (File):

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
            report_id=report_id,
            name=name,
            size_bytes=size_bytes,
            sha256=sha256,
            x_gregale_operation_job_run_id=x_gregale_operation_job_run_id,
            x_gregale_operation_job_instance_id=x_gregale_operation_job_instance_id,
            x_gregale_operation_generation=x_gregale_operation_generation,
            x_gregale_operation_attempt=x_gregale_operation_attempt,
        )
    ).parsed
