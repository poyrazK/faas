from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_artifact_upload_response import OperationArtifactUploadResponse
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
    x_faas_invocation_id: UUID,
    x_gregale_operation_capability: str,
    x_gregale_operation_attempt: int,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Faas-Invocation-Id"] = str(x_faas_invocation_id)

    headers["X-Gregale-Operation-Capability"] = x_gregale_operation_capability

    headers["X-Gregale-Operation-Attempt"] = str(x_gregale_operation_attempt)

    params: dict[str, Any] = {}

    params["report_id"] = report_id

    params["name"] = name

    params["size_bytes"] = size_bytes

    params["sha256"] = sha256

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/runtime/operations/{id}/artifact-uploads".format(
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
) -> OperationArtifactUploadResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationArtifactUploadResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[OperationArtifactUploadResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: File,
    report_id: str,
    name: str,
    size_bytes: int,
    sha256: str,
    x_faas_invocation_id: UUID,
    x_gregale_operation_capability: str,
    x_gregale_operation_attempt: int,
) -> Response[OperationArtifactUploadResponse | Problem]:
    """Upload direct private file bytes with current HTTP invocation authority.

     Stable report ID, name, exact size and SHA-256 bind a private upload receipt to the current HTTP
    invocation attempt. Requires a workload JWT for gregale:operations plus current invocation, attempt
    and capability. No bucket, source URI, provider credential or storage key is accepted. Bytes are
    verified under existing artifact quotas and transfer budgets. Each copy reserves fresh staging
    storage; concurrent copies converge and losing objects are cleaned up. Receipt lookup and committed
    replay do not repeat business code. New attachments are fenced by cancellation and live dispatch
    authority. Files download only after confirmed HTTP success or existing explicit success recovery;
    retaining bytes never settles the invocation.

    Args:
        id (UUID):
        report_id (str):
        name (str):
        size_bytes (int):
        sha256 (str):
        x_faas_invocation_id (UUID):
        x_gregale_operation_capability (str):
        x_gregale_operation_attempt (int):
        body (File):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationArtifactUploadResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        report_id=report_id,
        name=name,
        size_bytes=size_bytes,
        sha256=sha256,
        x_faas_invocation_id=x_faas_invocation_id,
        x_gregale_operation_capability=x_gregale_operation_capability,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: File,
    report_id: str,
    name: str,
    size_bytes: int,
    sha256: str,
    x_faas_invocation_id: UUID,
    x_gregale_operation_capability: str,
    x_gregale_operation_attempt: int,
) -> OperationArtifactUploadResponse | Problem | None:
    """Upload direct private file bytes with current HTTP invocation authority.

     Stable report ID, name, exact size and SHA-256 bind a private upload receipt to the current HTTP
    invocation attempt. Requires a workload JWT for gregale:operations plus current invocation, attempt
    and capability. No bucket, source URI, provider credential or storage key is accepted. Bytes are
    verified under existing artifact quotas and transfer budgets. Each copy reserves fresh staging
    storage; concurrent copies converge and losing objects are cleaned up. Receipt lookup and committed
    replay do not repeat business code. New attachments are fenced by cancellation and live dispatch
    authority. Files download only after confirmed HTTP success or existing explicit success recovery;
    retaining bytes never settles the invocation.

    Args:
        id (UUID):
        report_id (str):
        name (str):
        size_bytes (int):
        sha256 (str):
        x_faas_invocation_id (UUID):
        x_gregale_operation_capability (str):
        x_gregale_operation_attempt (int):
        body (File):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationArtifactUploadResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        report_id=report_id,
        name=name,
        size_bytes=size_bytes,
        sha256=sha256,
        x_faas_invocation_id=x_faas_invocation_id,
        x_gregale_operation_capability=x_gregale_operation_capability,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: File,
    report_id: str,
    name: str,
    size_bytes: int,
    sha256: str,
    x_faas_invocation_id: UUID,
    x_gregale_operation_capability: str,
    x_gregale_operation_attempt: int,
) -> Response[OperationArtifactUploadResponse | Problem]:
    """Upload direct private file bytes with current HTTP invocation authority.

     Stable report ID, name, exact size and SHA-256 bind a private upload receipt to the current HTTP
    invocation attempt. Requires a workload JWT for gregale:operations plus current invocation, attempt
    and capability. No bucket, source URI, provider credential or storage key is accepted. Bytes are
    verified under existing artifact quotas and transfer budgets. Each copy reserves fresh staging
    storage; concurrent copies converge and losing objects are cleaned up. Receipt lookup and committed
    replay do not repeat business code. New attachments are fenced by cancellation and live dispatch
    authority. Files download only after confirmed HTTP success or existing explicit success recovery;
    retaining bytes never settles the invocation.

    Args:
        id (UUID):
        report_id (str):
        name (str):
        size_bytes (int):
        sha256 (str):
        x_faas_invocation_id (UUID):
        x_gregale_operation_capability (str):
        x_gregale_operation_attempt (int):
        body (File):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationArtifactUploadResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        report_id=report_id,
        name=name,
        size_bytes=size_bytes,
        sha256=sha256,
        x_faas_invocation_id=x_faas_invocation_id,
        x_gregale_operation_capability=x_gregale_operation_capability,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: File,
    report_id: str,
    name: str,
    size_bytes: int,
    sha256: str,
    x_faas_invocation_id: UUID,
    x_gregale_operation_capability: str,
    x_gregale_operation_attempt: int,
) -> OperationArtifactUploadResponse | Problem | None:
    """Upload direct private file bytes with current HTTP invocation authority.

     Stable report ID, name, exact size and SHA-256 bind a private upload receipt to the current HTTP
    invocation attempt. Requires a workload JWT for gregale:operations plus current invocation, attempt
    and capability. No bucket, source URI, provider credential or storage key is accepted. Bytes are
    verified under existing artifact quotas and transfer budgets. Each copy reserves fresh staging
    storage; concurrent copies converge and losing objects are cleaned up. Receipt lookup and committed
    replay do not repeat business code. New attachments are fenced by cancellation and live dispatch
    authority. Files download only after confirmed HTTP success or existing explicit success recovery;
    retaining bytes never settles the invocation.

    Args:
        id (UUID):
        report_id (str):
        name (str):
        size_bytes (int):
        sha256 (str):
        x_faas_invocation_id (UUID):
        x_gregale_operation_capability (str):
        x_gregale_operation_attempt (int):
        body (File):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationArtifactUploadResponse | Problem
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
            x_faas_invocation_id=x_faas_invocation_id,
            x_gregale_operation_capability=x_gregale_operation_capability,
            x_gregale_operation_attempt=x_gregale_operation_attempt,
        )
    ).parsed
