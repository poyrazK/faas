from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_delivery_retry_request import OperationDeliveryRetryRequest
from ...models.operation_delivery_retry_response import OperationDeliveryRetryResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: OperationDeliveryRetryRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/operations/{id}/delivery-retries".format(
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
) -> Any | OperationDeliveryRetryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationDeliveryRetryResponse.from_dict(response.json())

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
        response_410 = cast(Any, None)
        return response_410

    if response.status_code == 413:
        response_413 = cast(Any, None)
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
) -> Response[Any | OperationDeliveryRetryResponse | Problem]:
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
    body: OperationDeliveryRetryRequest,
) -> Response[Any | OperationDeliveryRetryResponse | Problem]:
    """Commit an idempotent decision to retry a dead completion delivery.

     Requires account deploy write scope and MFA. Business work is never restarted. Retry IDs are scoped
    to the retained operation, with at most 32 decisions. An exact replay returns the immutable original
    receipt even after later delivery failures or delivery cleanup. Changed payloads and stale replay
    generations conflict. Receipt insertion and notification reset are atomic; receiver cooldowns and
    destination policy remain in force. Queued denotes the recorded decision, not live delivery status.

    Args:
        slug (str):
        id (UUID):
        body (OperationDeliveryRetryRequest): Stable retry identity and the observed dead delivery
            generation; explicit generation zero is valid.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | OperationDeliveryRetryResponse | Problem]
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
    body: OperationDeliveryRetryRequest,
) -> Any | OperationDeliveryRetryResponse | Problem | None:
    """Commit an idempotent decision to retry a dead completion delivery.

     Requires account deploy write scope and MFA. Business work is never restarted. Retry IDs are scoped
    to the retained operation, with at most 32 decisions. An exact replay returns the immutable original
    receipt even after later delivery failures or delivery cleanup. Changed payloads and stale replay
    generations conflict. Receipt insertion and notification reset are atomic; receiver cooldowns and
    destination policy remain in force. Queued denotes the recorded decision, not live delivery status.

    Args:
        slug (str):
        id (UUID):
        body (OperationDeliveryRetryRequest): Stable retry identity and the observed dead delivery
            generation; explicit generation zero is valid.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | OperationDeliveryRetryResponse | Problem
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
    body: OperationDeliveryRetryRequest,
) -> Response[Any | OperationDeliveryRetryResponse | Problem]:
    """Commit an idempotent decision to retry a dead completion delivery.

     Requires account deploy write scope and MFA. Business work is never restarted. Retry IDs are scoped
    to the retained operation, with at most 32 decisions. An exact replay returns the immutable original
    receipt even after later delivery failures or delivery cleanup. Changed payloads and stale replay
    generations conflict. Receipt insertion and notification reset are atomic; receiver cooldowns and
    destination policy remain in force. Queued denotes the recorded decision, not live delivery status.

    Args:
        slug (str):
        id (UUID):
        body (OperationDeliveryRetryRequest): Stable retry identity and the observed dead delivery
            generation; explicit generation zero is valid.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | OperationDeliveryRetryResponse | Problem]
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
    body: OperationDeliveryRetryRequest,
) -> Any | OperationDeliveryRetryResponse | Problem | None:
    """Commit an idempotent decision to retry a dead completion delivery.

     Requires account deploy write scope and MFA. Business work is never restarted. Retry IDs are scoped
    to the retained operation, with at most 32 decisions. An exact replay returns the immutable original
    receipt even after later delivery failures or delivery cleanup. Changed payloads and stale replay
    generations conflict. Receipt insertion and notification reset are atomic; receiver cooldowns and
    destination policy remain in force. Queued denotes the recorded decision, not live delivery status.

    Args:
        slug (str):
        id (UUID):
        body (OperationDeliveryRetryRequest): Stable retry identity and the observed dead delivery
            generation; explicit generation zero is valid.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | OperationDeliveryRetryResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
