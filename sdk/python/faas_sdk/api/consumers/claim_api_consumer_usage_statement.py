from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_consumer_usage_statement_handoff_response import APIConsumerUsageStatementHandoffResponse
from ...models.claim_api_consumer_usage_statement_request import ClaimAPIConsumerUsageStatementRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    consumer_id: UUID,
    statement_id: UUID,
    *,
    body: ClaimAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/consumers/{consumer_id}/usage-statements/{statement_id}/handoff".format(
            slug=quote(str(slug), safe=""),
            consumer_id=quote(str(consumer_id), safe=""),
            statement_id=quote(str(statement_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIConsumerUsageStatementHandoffResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APIConsumerUsageStatementHandoffResponse.from_dict(response.json())

        return response_200

    if response.status_code == 201:
        response_201 = APIConsumerUsageStatementHandoffResponse.from_dict(response.json())

        return response_201

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APIConsumerUsageStatementHandoffResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    consumer_id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ClaimAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[APIConsumerUsageStatementHandoffResponse | Problem]:
    """Record a customer billing handoff for a finalized statement.

     Claims a finalized usage statement for the customer's own billing system. Repeating the same claim
    returns the original receipt; a statement or external invoice ID cannot be claimed twice.

    Args:
        slug (str):
        consumer_id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (ClaimAPIConsumerUsageStatementRequest): Customer billing system's external invoice
            reference for a finalized statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerUsageStatementHandoffResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        statement_id=statement_id,
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
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ClaimAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> APIConsumerUsageStatementHandoffResponse | Problem | None:
    """Record a customer billing handoff for a finalized statement.

     Claims a finalized usage statement for the customer's own billing system. Repeating the same claim
    returns the original receipt; a statement or external invoice ID cannot be claimed twice.

    Args:
        slug (str):
        consumer_id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (ClaimAPIConsumerUsageStatementRequest): Customer billing system's external invoice
            reference for a finalized statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerUsageStatementHandoffResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        consumer_id=consumer_id,
        statement_id=statement_id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    consumer_id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ClaimAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[APIConsumerUsageStatementHandoffResponse | Problem]:
    """Record a customer billing handoff for a finalized statement.

     Claims a finalized usage statement for the customer's own billing system. Repeating the same claim
    returns the original receipt; a statement or external invoice ID cannot be claimed twice.

    Args:
        slug (str):
        consumer_id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (ClaimAPIConsumerUsageStatementRequest): Customer billing system's external invoice
            reference for a finalized statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerUsageStatementHandoffResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        statement_id=statement_id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    consumer_id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ClaimAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> APIConsumerUsageStatementHandoffResponse | Problem | None:
    """Record a customer billing handoff for a finalized statement.

     Claims a finalized usage statement for the customer's own billing system. Repeating the same claim
    returns the original receipt; a statement or external invoice ID cannot be claimed twice.

    Args:
        slug (str):
        consumer_id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):
        body (ClaimAPIConsumerUsageStatementRequest): Customer billing system's external invoice
            reference for a finalized statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerUsageStatementHandoffResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            consumer_id=consumer_id,
            statement_id=statement_id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
