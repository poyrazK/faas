from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.publish_event_request import PublishEventRequest
from ...models.publish_event_response import PublishEventResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: PublishEventRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/events:publish",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | PublishEventResponse | None:
    if response.status_code == 202:
        response_202 = PublishEventResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[Problem | PublishEventResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: PublishEventRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | PublishEventResponse]:
    """Publish one tenant-scoped internal event.

     Persists a canonical CloudEvents-shaped envelope for later content
    matching and delivery. The authenticated account owns the event;
    accountid is server-stamped and a supplied value must match it.
    Event identity is unique per account and source; reusing an id with
    different type, schema version, or data returns 409. Older snake_case input attribute
    names remain accepted during migration. API keys require
    `events:publish`, `deploy:write`, or `admin`.
    Matching and delivery are asynchronous follow-up work. The Location header
    and receipt_url point to GET /v1/events/receipt for delivery evidence.
    Identical identity retries return the original accepted_at timestamp,
    including when retained customer event storage is full. New identities
    exceeding the account count or byte budget return 429 with code
    event_storage_capacity_exhausted, limit/observed and Retry-After. No ledger
    or receipt is committed on rejection. Storage is released by receipt
    retention pruning, not by delivery completion. GET /v1/events/storage
    reports current usage and plan limits.

    Args:
        idempotency_key (str | Unset):
        body (PublishEventRequest): Caller-authored envelope for the tenant-scoped internal event
            router.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | PublishEventResponse]
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
    client: AuthenticatedClient,
    body: PublishEventRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | PublishEventResponse | None:
    """Publish one tenant-scoped internal event.

     Persists a canonical CloudEvents-shaped envelope for later content
    matching and delivery. The authenticated account owns the event;
    accountid is server-stamped and a supplied value must match it.
    Event identity is unique per account and source; reusing an id with
    different type, schema version, or data returns 409. Older snake_case input attribute
    names remain accepted during migration. API keys require
    `events:publish`, `deploy:write`, or `admin`.
    Matching and delivery are asynchronous follow-up work. The Location header
    and receipt_url point to GET /v1/events/receipt for delivery evidence.
    Identical identity retries return the original accepted_at timestamp,
    including when retained customer event storage is full. New identities
    exceeding the account count or byte budget return 429 with code
    event_storage_capacity_exhausted, limit/observed and Retry-After. No ledger
    or receipt is committed on rejection. Storage is released by receipt
    retention pruning, not by delivery completion. GET /v1/events/storage
    reports current usage and plan limits.

    Args:
        idempotency_key (str | Unset):
        body (PublishEventRequest): Caller-authored envelope for the tenant-scoped internal event
            router.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | PublishEventResponse
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: PublishEventRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | PublishEventResponse]:
    """Publish one tenant-scoped internal event.

     Persists a canonical CloudEvents-shaped envelope for later content
    matching and delivery. The authenticated account owns the event;
    accountid is server-stamped and a supplied value must match it.
    Event identity is unique per account and source; reusing an id with
    different type, schema version, or data returns 409. Older snake_case input attribute
    names remain accepted during migration. API keys require
    `events:publish`, `deploy:write`, or `admin`.
    Matching and delivery are asynchronous follow-up work. The Location header
    and receipt_url point to GET /v1/events/receipt for delivery evidence.
    Identical identity retries return the original accepted_at timestamp,
    including when retained customer event storage is full. New identities
    exceeding the account count or byte budget return 429 with code
    event_storage_capacity_exhausted, limit/observed and Retry-After. No ledger
    or receipt is committed on rejection. Storage is released by receipt
    retention pruning, not by delivery completion. GET /v1/events/storage
    reports current usage and plan limits.

    Args:
        idempotency_key (str | Unset):
        body (PublishEventRequest): Caller-authored envelope for the tenant-scoped internal event
            router.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | PublishEventResponse]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: PublishEventRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | PublishEventResponse | None:
    """Publish one tenant-scoped internal event.

     Persists a canonical CloudEvents-shaped envelope for later content
    matching and delivery. The authenticated account owns the event;
    accountid is server-stamped and a supplied value must match it.
    Event identity is unique per account and source; reusing an id with
    different type, schema version, or data returns 409. Older snake_case input attribute
    names remain accepted during migration. API keys require
    `events:publish`, `deploy:write`, or `admin`.
    Matching and delivery are asynchronous follow-up work. The Location header
    and receipt_url point to GET /v1/events/receipt for delivery evidence.
    Identical identity retries return the original accepted_at timestamp,
    including when retained customer event storage is full. New identities
    exceeding the account count or byte budget return 429 with code
    event_storage_capacity_exhausted, limit/observed and Retry-After. No ledger
    or receipt is committed on rejection. Storage is released by receipt
    retention pruning, not by delivery completion. GET /v1/events/storage
    reports current usage and plan limits.

    Args:
        idempotency_key (str | Unset):
        body (PublishEventRequest): Caller-authored envelope for the tenant-scoped internal event
            router.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | PublishEventResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
