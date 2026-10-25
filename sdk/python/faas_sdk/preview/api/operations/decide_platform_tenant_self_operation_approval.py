from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_approval_request import OperationApprovalRequest
from ...models.operation_response import OperationResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    step: str,
    *,
    body: OperationApprovalRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/platform-tenant-self/customer-operations/{id}/approvals/{step}".format(
            id=quote(str(id), safe=""),
            step=quote(str(step), safe=""),
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
    step: str,
    *,
    client: AuthenticatedClient | Client,
    body: OperationApprovalRequest,
) -> Response[OperationResponse | Problem]:
    """Decide this customer's approval on its waiting operation (ADR-1194).

     Requires platform_tenant:operations:manage. Only a step whose definition names the customer
    approver, on the customer's own operation. The same decision is idempotent; a different one
    conflicts.

    Args:
        id (UUID):
        step (str):
        body (OperationApprovalRequest): One approval decision for the operation's current
            generation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        step=step,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    step: str,
    *,
    client: AuthenticatedClient | Client,
    body: OperationApprovalRequest,
) -> OperationResponse | Problem | None:
    """Decide this customer's approval on its waiting operation (ADR-1194).

     Requires platform_tenant:operations:manage. Only a step whose definition names the customer
    approver, on the customer's own operation. The same decision is idempotent; a different one
    conflicts.

    Args:
        id (UUID):
        step (str):
        body (OperationApprovalRequest): One approval decision for the operation's current
            generation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationResponse | Problem
    """

    return sync_detailed(
        id=id,
        step=step,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    step: str,
    *,
    client: AuthenticatedClient | Client,
    body: OperationApprovalRequest,
) -> Response[OperationResponse | Problem]:
    """Decide this customer's approval on its waiting operation (ADR-1194).

     Requires platform_tenant:operations:manage. Only a step whose definition names the customer
    approver, on the customer's own operation. The same decision is idempotent; a different one
    conflicts.

    Args:
        id (UUID):
        step (str):
        body (OperationApprovalRequest): One approval decision for the operation's current
            generation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        step=step,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    step: str,
    *,
    client: AuthenticatedClient | Client,
    body: OperationApprovalRequest,
) -> OperationResponse | Problem | None:
    """Decide this customer's approval on its waiting operation (ADR-1194).

     Requires platform_tenant:operations:manage. Only a step whose definition names the customer
    approver, on the customer's own operation. The same decision is idempotent; a different one
    conflicts.

    Args:
        id (UUID):
        step (str):
        body (OperationApprovalRequest): One approval decision for the operation's current
            generation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            step=step,
            client=client,
            body=body,
        )
    ).parsed
