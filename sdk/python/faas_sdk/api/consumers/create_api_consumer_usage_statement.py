from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_consumer_usage_statement_response import APIConsumerUsageStatementResponse
from ...models.create_api_consumer_usage_statement_request import CreateAPIConsumerUsageStatementRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    consumer_id: UUID,
    *,
    body: CreateAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/consumers/{consumer_id}/usage-statements".format(
            slug=quote(str(slug), safe=""),
            consumer_id=quote(str(consumer_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIConsumerUsageStatementResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APIConsumerUsageStatementResponse.from_dict(response.json())

        return response_200

    if response.status_code == 201:
        response_201 = APIConsumerUsageStatementResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APIConsumerUsageStatementResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[APIConsumerUsageStatementResponse | Problem]:
    """Snapshot an API consumer usage quote.

     Creates an immutable, auditable statement for the explicit UTC-minute period; repeating the period
    returns the original snapshot.

    Args:
        slug (str):
        consumer_id (UUID):
        idempotency_key (str | Unset):
        body (CreateAPIConsumerUsageStatementRequest): Explicit UTC-minute period to snapshot as
            an immutable usage statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerUsageStatementResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> APIConsumerUsageStatementResponse | Problem | None:
    """Snapshot an API consumer usage quote.

     Creates an immutable, auditable statement for the explicit UTC-minute period; repeating the period
    returns the original snapshot.

    Args:
        slug (str):
        consumer_id (UUID):
        idempotency_key (str | Unset):
        body (CreateAPIConsumerUsageStatementRequest): Explicit UTC-minute period to snapshot as
            an immutable usage statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerUsageStatementResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        consumer_id=consumer_id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[APIConsumerUsageStatementResponse | Problem]:
    """Snapshot an API consumer usage quote.

     Creates an immutable, auditable statement for the explicit UTC-minute period; repeating the period
    returns the original snapshot.

    Args:
        slug (str):
        consumer_id (UUID):
        idempotency_key (str | Unset):
        body (CreateAPIConsumerUsageStatementRequest): Explicit UTC-minute period to snapshot as
            an immutable usage statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerUsageStatementResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> APIConsumerUsageStatementResponse | Problem | None:
    """Snapshot an API consumer usage quote.

     Creates an immutable, auditable statement for the explicit UTC-minute period; repeating the period
    returns the original snapshot.

    Args:
        slug (str):
        consumer_id (UUID):
        idempotency_key (str | Unset):
        body (CreateAPIConsumerUsageStatementRequest): Explicit UTC-minute period to snapshot as
            an immutable usage statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerUsageStatementResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            consumer_id=consumer_id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
