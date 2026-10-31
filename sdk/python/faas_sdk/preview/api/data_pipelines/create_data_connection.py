from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.create_data_connection_request import CreateDataConnectionRequest
from ...models.data_connection import DataConnection
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: CreateDataConnectionRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/data/connections",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> DataConnection | Problem:
    if response.status_code == 201:
        response_201 = DataConnection.from_dict(response.json())

        return response_201

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DataConnection | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateDataConnectionRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[DataConnection | Problem]:
    """Seal a credential for a PostgreSQL pipeline source

    Args:
        idempotency_key (str | Unset):
        body (CreateDataConnectionRequest): Source registration containing a verified-TLS
            PostgreSQL URL for sealing.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataConnection | Problem]
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
    body: CreateDataConnectionRequest,
    idempotency_key: str | Unset = UNSET,
) -> DataConnection | Problem | None:
    """Seal a credential for a PostgreSQL pipeline source

    Args:
        idempotency_key (str | Unset):
        body (CreateDataConnectionRequest): Source registration containing a verified-TLS
            PostgreSQL URL for sealing.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataConnection | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateDataConnectionRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[DataConnection | Problem]:
    """Seal a credential for a PostgreSQL pipeline source

    Args:
        idempotency_key (str | Unset):
        body (CreateDataConnectionRequest): Source registration containing a verified-TLS
            PostgreSQL URL for sealing.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataConnection | Problem]
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
    body: CreateDataConnectionRequest,
    idempotency_key: str | Unset = UNSET,
) -> DataConnection | Problem | None:
    """Seal a credential for a PostgreSQL pipeline source

    Args:
        idempotency_key (str | Unset):
        body (CreateDataConnectionRequest): Source registration containing a verified-TLS
            PostgreSQL URL for sealing.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataConnection | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
