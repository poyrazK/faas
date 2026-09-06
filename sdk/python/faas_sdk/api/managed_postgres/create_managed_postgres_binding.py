from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.create_managed_postgres_binding_request import CreateManagedPostgresBindingRequest
from ...models.managed_postgres_binding import ManagedPostgresBinding
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    body: CreateManagedPostgresBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/postgres/databases/{id}/bindings".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresBinding | Problem:
    if response.status_code == 201:
        response_201 = ManagedPostgresBinding.from_dict(response.json())

        return response_201

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresBinding | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateManagedPostgresBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedPostgresBinding | Problem]:
    """Bind a workload app to a database

    Args:
        id (str):
        idempotency_key (str | Unset):
        body (CreateManagedPostgresBindingRequest): Request to inject a managed database
            credential into an app environment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresBinding | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateManagedPostgresBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> ManagedPostgresBinding | Problem | None:
    """Bind a workload app to a database

    Args:
        id (str):
        idempotency_key (str | Unset):
        body (CreateManagedPostgresBindingRequest): Request to inject a managed database
            credential into an app environment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresBinding | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateManagedPostgresBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedPostgresBinding | Problem]:
    """Bind a workload app to a database

    Args:
        id (str):
        idempotency_key (str | Unset):
        body (CreateManagedPostgresBindingRequest): Request to inject a managed database
            credential into an app environment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresBinding | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateManagedPostgresBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> ManagedPostgresBinding | Problem | None:
    """Bind a workload app to a database

    Args:
        id (str):
        idempotency_key (str | Unset):
        body (CreateManagedPostgresBindingRequest): Request to inject a managed database
            credential into an app environment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresBinding | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
