from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_recovery_preview import OperationRecoveryPreview
from ...models.operation_recovery_preview_request import OperationRecoveryPreviewRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: OperationRecoveryPreviewRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/operations/{id}/recovery-preview".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationRecoveryPreview | Problem | None:
    if response.status_code == 200:
        response_200 = OperationRecoveryPreview.from_dict(response.json())

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
) -> Response[OperationRecoveryPreview | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: OperationRecoveryPreviewRequest,
) -> Response[OperationRecoveryPreview | Problem]:
    """Preview an operation recovery decision.

     Account read scope and MFA are required. Validates a proposed resolution without starting work,
    recording a receipt, consuming quotas or publishing files. Workflow retries use the native resume
    planner. Eligible means current platform checks pass; external effects still require operator
    evidence. Apply revalidates under writer locks. Expected generation rejects stale previews; the
    inspection revision can optionally fence the later apply request. Available while new admission is
    closed.

    Args:
        slug (str):
        id (UUID):
        body (OperationRecoveryPreviewRequest): Proposed recovery resolution without a durable
            decision identity or reconciliation evidence.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationRecoveryPreview | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: OperationRecoveryPreviewRequest,
) -> OperationRecoveryPreview | Problem | None:
    """Preview an operation recovery decision.

     Account read scope and MFA are required. Validates a proposed resolution without starting work,
    recording a receipt, consuming quotas or publishing files. Workflow retries use the native resume
    planner. Eligible means current platform checks pass; external effects still require operator
    evidence. Apply revalidates under writer locks. Expected generation rejects stale previews; the
    inspection revision can optionally fence the later apply request. Available while new admission is
    closed.

    Args:
        slug (str):
        id (UUID):
        body (OperationRecoveryPreviewRequest): Proposed recovery resolution without a durable
            decision identity or reconciliation evidence.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationRecoveryPreview | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: OperationRecoveryPreviewRequest,
) -> Response[OperationRecoveryPreview | Problem]:
    """Preview an operation recovery decision.

     Account read scope and MFA are required. Validates a proposed resolution without starting work,
    recording a receipt, consuming quotas or publishing files. Workflow retries use the native resume
    planner. Eligible means current platform checks pass; external effects still require operator
    evidence. Apply revalidates under writer locks. Expected generation rejects stale previews; the
    inspection revision can optionally fence the later apply request. Available while new admission is
    closed.

    Args:
        slug (str):
        id (UUID):
        body (OperationRecoveryPreviewRequest): Proposed recovery resolution without a durable
            decision identity or reconciliation evidence.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationRecoveryPreview | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: OperationRecoveryPreviewRequest,
) -> OperationRecoveryPreview | Problem | None:
    """Preview an operation recovery decision.

     Account read scope and MFA are required. Validates a proposed resolution without starting work,
    recording a receipt, consuming quotas or publishing files. Workflow retries use the native resume
    planner. Eligible means current platform checks pass; external effects still require operator
    evidence. Apply revalidates under writer locks. Expected generation rejects stale previews; the
    inspection revision can optionally fence the later apply request. Available while new admission is
    closed.

    Args:
        slug (str):
        id (UUID):
        body (OperationRecoveryPreviewRequest): Proposed recovery resolution without a durable
            decision identity or reconciliation evidence.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationRecoveryPreview | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
