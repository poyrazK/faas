from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_accepted_response import OperationAcceptedResponse
from ...models.operation_start_request import OperationStartRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: OperationStartRequest,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/platform-tenant-self/customer-operations",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationAcceptedResponse | Problem | None:
    if response.status_code == 202:
        response_202 = OperationAcceptedResponse.from_dict(response.json())

        return response_202

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
) -> Response[OperationAcceptedResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: OperationStartRequest,
    idempotency_key: str,
) -> Response[OperationAcceptedResponse | Problem]:
    """Start work owned by the authenticated customer.

     Requires platform_tenant:operations:manage and an active customer identity. Idempotency-Key is
    required and scoped to account, app, environment, authenticated platform tenant and operation name.
    Canonically equivalent JSON inputs reuse the same operation across deployment changes; different
    input conflicts. Retention follows the admitted plan: Hobby/Pro/Scale results 7/30/90 days and
    deduplication 30/90/180 days. An expired retained result returns 410 during the deduplication
    window. Fresh work is unavailable on Free. Production admission remains disabled in this staged API.

    Args:
        idempotency_key (str):
        body (OperationStartRequest): Submission owned by the authenticated platform tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationAcceptedResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: OperationStartRequest,
    idempotency_key: str,
) -> OperationAcceptedResponse | Problem | None:
    """Start work owned by the authenticated customer.

     Requires platform_tenant:operations:manage and an active customer identity. Idempotency-Key is
    required and scoped to account, app, environment, authenticated platform tenant and operation name.
    Canonically equivalent JSON inputs reuse the same operation across deployment changes; different
    input conflicts. Retention follows the admitted plan: Hobby/Pro/Scale results 7/30/90 days and
    deduplication 30/90/180 days. An expired retained result returns 410 during the deduplication
    window. Fresh work is unavailable on Free. Production admission remains disabled in this staged API.

    Args:
        idempotency_key (str):
        body (OperationStartRequest): Submission owned by the authenticated platform tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationAcceptedResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: OperationStartRequest,
    idempotency_key: str,
) -> Response[OperationAcceptedResponse | Problem]:
    """Start work owned by the authenticated customer.

     Requires platform_tenant:operations:manage and an active customer identity. Idempotency-Key is
    required and scoped to account, app, environment, authenticated platform tenant and operation name.
    Canonically equivalent JSON inputs reuse the same operation across deployment changes; different
    input conflicts. Retention follows the admitted plan: Hobby/Pro/Scale results 7/30/90 days and
    deduplication 30/90/180 days. An expired retained result returns 410 during the deduplication
    window. Fresh work is unavailable on Free. Production admission remains disabled in this staged API.

    Args:
        idempotency_key (str):
        body (OperationStartRequest): Submission owned by the authenticated platform tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationAcceptedResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: OperationStartRequest,
    idempotency_key: str,
) -> OperationAcceptedResponse | Problem | None:
    """Start work owned by the authenticated customer.

     Requires platform_tenant:operations:manage and an active customer identity. Idempotency-Key is
    required and scoped to account, app, environment, authenticated platform tenant and operation name.
    Canonically equivalent JSON inputs reuse the same operation across deployment changes; different
    input conflicts. Retention follows the admitted plan: Hobby/Pro/Scale results 7/30/90 days and
    deduplication 30/90/180 days. An expired retained result returns 410 during the deduplication
    window. Fresh work is unavailable on Free. Production admission remains disabled in this staged API.

    Args:
        idempotency_key (str):
        body (OperationStartRequest): Submission owned by the authenticated platform tenant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationAcceptedResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
