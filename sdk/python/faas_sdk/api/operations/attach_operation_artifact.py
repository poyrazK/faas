from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_artifact_request import OperationArtifactRequest
from ...models.operation_response import OperationResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: OperationArtifactRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Faas-Invocation-Id"] = x_faas_invocation_id

    headers["X-Gregale-Operation-Attempt"] = str(x_gregale_operation_attempt)

    headers["X-Gregale-Operation-Capability"] = x_gregale_operation_capability

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/runtime/operations/{id}/artifacts".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationResponse.from_dict(response.json())

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
) -> Response[OperationResponse | Problem]:
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
    body: OperationArtifactRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> Response[OperationResponse | Problem]:
    """Attach verified managed-object result bytes.

     Requires workload JWT and current dispatch proof as for progress. The object must be in a ready,
    private, account/app/environment-owned managed bucket. Exact size and SHA-256 are verified before
    the attachment and durable event commit. Repeated report_id with the same declaration is idempotent.
    A verified copy is retained under a private platform key; subsequent source mutation or deletion
    does not change the retained result.

    Args:
        id (UUID):
        x_faas_invocation_id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_capability (str):
        body (OperationArtifactRequest): Idempotent attachment of an existing managed object;
            bytes are checked before commit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        x_faas_invocation_id=x_faas_invocation_id,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
        x_gregale_operation_capability=x_gregale_operation_capability,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: OperationArtifactRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> OperationResponse | Problem | None:
    """Attach verified managed-object result bytes.

     Requires workload JWT and current dispatch proof as for progress. The object must be in a ready,
    private, account/app/environment-owned managed bucket. Exact size and SHA-256 are verified before
    the attachment and durable event commit. Repeated report_id with the same declaration is idempotent.
    A verified copy is retained under a private platform key; subsequent source mutation or deletion
    does not change the retained result.

    Args:
        id (UUID):
        x_faas_invocation_id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_capability (str):
        body (OperationArtifactRequest): Idempotent attachment of an existing managed object;
            bytes are checked before commit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        x_faas_invocation_id=x_faas_invocation_id,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
        x_gregale_operation_capability=x_gregale_operation_capability,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: OperationArtifactRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> Response[OperationResponse | Problem]:
    """Attach verified managed-object result bytes.

     Requires workload JWT and current dispatch proof as for progress. The object must be in a ready,
    private, account/app/environment-owned managed bucket. Exact size and SHA-256 are verified before
    the attachment and durable event commit. Repeated report_id with the same declaration is idempotent.
    A verified copy is retained under a private platform key; subsequent source mutation or deletion
    does not change the retained result.

    Args:
        id (UUID):
        x_faas_invocation_id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_capability (str):
        body (OperationArtifactRequest): Idempotent attachment of an existing managed object;
            bytes are checked before commit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        x_faas_invocation_id=x_faas_invocation_id,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
        x_gregale_operation_capability=x_gregale_operation_capability,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: OperationArtifactRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> OperationResponse | Problem | None:
    """Attach verified managed-object result bytes.

     Requires workload JWT and current dispatch proof as for progress. The object must be in a ready,
    private, account/app/environment-owned managed bucket. Exact size and SHA-256 are verified before
    the attachment and durable event commit. Repeated report_id with the same declaration is idempotent.
    A verified copy is retained under a private platform key; subsequent source mutation or deletion
    does not change the retained result.

    Args:
        id (UUID):
        x_faas_invocation_id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_capability (str):
        body (OperationArtifactRequest): Idempotent attachment of an existing managed object;
            bytes are checked before commit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            x_faas_invocation_id=x_faas_invocation_id,
            x_gregale_operation_attempt=x_gregale_operation_attempt,
            x_gregale_operation_capability=x_gregale_operation_capability,
        )
    ).parsed
