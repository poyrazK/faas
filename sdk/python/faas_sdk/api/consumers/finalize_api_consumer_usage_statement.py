from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_consumer_usage_statement_response import APIConsumerUsageStatementResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    consumer_id: UUID,
    statement_id: UUID,
    *,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/consumers/{consumer_id}/usage-statements/{statement_id}/finalize".format(
            slug=quote(str(slug), safe=""),
            consumer_id=quote(str(consumer_id), safe=""),
            statement_id=quote(str(statement_id), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIConsumerUsageStatementResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APIConsumerUsageStatementResponse.from_dict(response.json())

        return response_200

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
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[APIConsumerUsageStatementResponse | Problem]:
    """Finalize a fully priced API consumer usage statement.

     Records the payable lifecycle transition; repeated calls are idempotent. A new transition emits a
    durable, signed usage_statement.finalized app webhook when a matching subscription exists.

    Args:
        slug (str):
        consumer_id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerUsageStatementResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        statement_id=statement_id,
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
    idempotency_key: str | Unset = UNSET,
) -> APIConsumerUsageStatementResponse | Problem | None:
    """Finalize a fully priced API consumer usage statement.

     Records the payable lifecycle transition; repeated calls are idempotent. A new transition emits a
    durable, signed usage_statement.finalized app webhook when a matching subscription exists.

    Args:
        slug (str):
        consumer_id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerUsageStatementResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        consumer_id=consumer_id,
        statement_id=statement_id,
        client=client,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    consumer_id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[APIConsumerUsageStatementResponse | Problem]:
    """Finalize a fully priced API consumer usage statement.

     Records the payable lifecycle transition; repeated calls are idempotent. A new transition emits a
    durable, signed usage_statement.finalized app webhook when a matching subscription exists.

    Args:
        slug (str):
        consumer_id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerUsageStatementResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        statement_id=statement_id,
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
    idempotency_key: str | Unset = UNSET,
) -> APIConsumerUsageStatementResponse | Problem | None:
    """Finalize a fully priced API consumer usage statement.

     Records the payable lifecycle transition; repeated calls are idempotent. A new transition emits a
    durable, signed usage_statement.finalized app webhook when a matching subscription exists.

    Args:
        slug (str):
        consumer_id (UUID):
        statement_id (UUID):
        idempotency_key (str | Unset):

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
            statement_id=statement_id,
            client=client,
            idempotency_key=idempotency_key,
        )
    ).parsed
