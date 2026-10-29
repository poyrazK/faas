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
    organization: str,
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
        "url": "/v1/orgs/{organization}/postgres/databases/{id}/bindings".format(
            organization=quote(str(organization), safe=""),
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

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateManagedPostgresBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedPostgresBinding | Problem]:
    """Attach the company database to an app environment.

     The app and database must belong to the addressed company. Their storage namespaces may differ.
    Owners, admins and developers may attach bindings with the required database scope.

    Args:
        organization (str):
        id (str):
        idempotency_key (str | Unset):
        body (CreateManagedPostgresBindingRequest): Request to inject a managed database
            credential into an app environment. Access values are provider-neutral; the selected
            database backend may support only a subset.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresBinding | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateManagedPostgresBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> ManagedPostgresBinding | Problem | None:
    """Attach the company database to an app environment.

     The app and database must belong to the addressed company. Their storage namespaces may differ.
    Owners, admins and developers may attach bindings with the required database scope.

    Args:
        organization (str):
        id (str):
        idempotency_key (str | Unset):
        body (CreateManagedPostgresBindingRequest): Request to inject a managed database
            credential into an app environment. Access values are provider-neutral; the selected
            database backend may support only a subset.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresBinding | Problem
    """

    return sync_detailed(
        organization=organization,
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateManagedPostgresBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedPostgresBinding | Problem]:
    """Attach the company database to an app environment.

     The app and database must belong to the addressed company. Their storage namespaces may differ.
    Owners, admins and developers may attach bindings with the required database scope.

    Args:
        organization (str):
        id (str):
        idempotency_key (str | Unset):
        body (CreateManagedPostgresBindingRequest): Request to inject a managed database
            credential into an app environment. Access values are provider-neutral; the selected
            database backend may support only a subset.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresBinding | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateManagedPostgresBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> ManagedPostgresBinding | Problem | None:
    """Attach the company database to an app environment.

     The app and database must belong to the addressed company. Their storage namespaces may differ.
    Owners, admins and developers may attach bindings with the required database scope.

    Args:
        organization (str):
        id (str):
        idempotency_key (str | Unset):
        body (CreateManagedPostgresBindingRequest): Request to inject a managed database
            credential into an app environment. Access values are provider-neutral; the selected
            database backend may support only a subset.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresBinding | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
