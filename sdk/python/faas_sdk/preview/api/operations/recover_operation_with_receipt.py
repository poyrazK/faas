from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_recovery_decision import OperationRecoveryDecision
from ...models.operation_recovery_request import OperationRecoveryRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: OperationRecoveryRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/operations/{id}/recover-receipt".format(
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
) -> OperationRecoveryDecision | Problem | None:
    if response.status_code == 200:
        response_200 = OperationRecoveryDecision.from_dict(response.json())

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
) -> Response[OperationRecoveryDecision | Problem]:
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
    body: OperationRecoveryRequest,
) -> Response[OperationRecoveryDecision | Problem]:
    """Record or replay an immutable recovery decision acknowledgement.

     Account-only; requires deploy:write and MFA when enabled. Uses the same evidence, generation,
    optional inspection revision, output, quota and pinned execution fences as recover. The decision is
    committed atomically with recovery. Matching recovery_id and canonical request replay the original
    decision even after work advances; changed intent returns 409. The acknowledgement describes state
    at acceptance, not current status or completed work. expires_at is the retention deadline before
    this decision; later recovery cannot extend it. Old accepted recoveries without retained decision
    acknowledgements return 409 operation_recovery_receipt_unavailable and are never repeated. Expired
    receipts return 410.

    Args:
        slug (str):
        id (UUID):
        body (OperationRecoveryRequest): Explicit recovery decision with retained evidence and
            idempotent recovery identity.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationRecoveryDecision | Problem]
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
    body: OperationRecoveryRequest,
) -> OperationRecoveryDecision | Problem | None:
    """Record or replay an immutable recovery decision acknowledgement.

     Account-only; requires deploy:write and MFA when enabled. Uses the same evidence, generation,
    optional inspection revision, output, quota and pinned execution fences as recover. The decision is
    committed atomically with recovery. Matching recovery_id and canonical request replay the original
    decision even after work advances; changed intent returns 409. The acknowledgement describes state
    at acceptance, not current status or completed work. expires_at is the retention deadline before
    this decision; later recovery cannot extend it. Old accepted recoveries without retained decision
    acknowledgements return 409 operation_recovery_receipt_unavailable and are never repeated. Expired
    receipts return 410.

    Args:
        slug (str):
        id (UUID):
        body (OperationRecoveryRequest): Explicit recovery decision with retained evidence and
            idempotent recovery identity.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationRecoveryDecision | Problem
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
    body: OperationRecoveryRequest,
) -> Response[OperationRecoveryDecision | Problem]:
    """Record or replay an immutable recovery decision acknowledgement.

     Account-only; requires deploy:write and MFA when enabled. Uses the same evidence, generation,
    optional inspection revision, output, quota and pinned execution fences as recover. The decision is
    committed atomically with recovery. Matching recovery_id and canonical request replay the original
    decision even after work advances; changed intent returns 409. The acknowledgement describes state
    at acceptance, not current status or completed work. expires_at is the retention deadline before
    this decision; later recovery cannot extend it. Old accepted recoveries without retained decision
    acknowledgements return 409 operation_recovery_receipt_unavailable and are never repeated. Expired
    receipts return 410.

    Args:
        slug (str):
        id (UUID):
        body (OperationRecoveryRequest): Explicit recovery decision with retained evidence and
            idempotent recovery identity.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationRecoveryDecision | Problem]
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
    body: OperationRecoveryRequest,
) -> OperationRecoveryDecision | Problem | None:
    """Record or replay an immutable recovery decision acknowledgement.

     Account-only; requires deploy:write and MFA when enabled. Uses the same evidence, generation,
    optional inspection revision, output, quota and pinned execution fences as recover. The decision is
    committed atomically with recovery. Matching recovery_id and canonical request replay the original
    decision even after work advances; changed intent returns 409. The acknowledgement describes state
    at acceptance, not current status or completed work. expires_at is the retention deadline before
    this decision; later recovery cannot extend it. Old accepted recoveries without retained decision
    acknowledgements return 409 operation_recovery_receipt_unavailable and are never repeated. Expired
    receipts return 410.

    Args:
        slug (str):
        id (UUID):
        body (OperationRecoveryRequest): Explicit recovery decision with retained evidence and
            idempotent recovery identity.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationRecoveryDecision | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
