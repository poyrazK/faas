from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_cutover_preflight import ManagedPostgresCutoverPreflight
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/postgres/cutovers/{id}/preflight".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresCutoverPreflight | Problem:
    if response.status_code == 202:
        response_202 = ManagedPostgresCutoverPreflight.from_dict(response.json())

        return response_202

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresCutoverPreflight | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedPostgresCutoverPreflight | Problem]:
    """Queue a restore activation preflight

     Requires managed PostgreSQL manage scope and verified email. Durably
    queues catalog inspection, including while provider provisioning is
    disabled. Concurrent pending requests keep the revision; requesting a
    completed check starts a new revision. Does not fence admissions, call
    a provider, publish bindings or resume workloads. Activation remains
    disabled. Idempotency replay returns the original response; poll GET
    for latest progress. Cancelling or cancelled cutovers conflict.

    Args:
        id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCutoverPreflight | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> ManagedPostgresCutoverPreflight | Problem | None:
    """Queue a restore activation preflight

     Requires managed PostgreSQL manage scope and verified email. Durably
    queues catalog inspection, including while provider provisioning is
    disabled. Concurrent pending requests keep the revision; requesting a
    completed check starts a new revision. Does not fence admissions, call
    a provider, publish bindings or resume workloads. Activation remains
    disabled. Idempotency replay returns the original response; poll GET
    for latest progress. Cancelling or cancelled cutovers conflict.

    Args:
        id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresCutoverPreflight | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedPostgresCutoverPreflight | Problem]:
    """Queue a restore activation preflight

     Requires managed PostgreSQL manage scope and verified email. Durably
    queues catalog inspection, including while provider provisioning is
    disabled. Concurrent pending requests keep the revision; requesting a
    completed check starts a new revision. Does not fence admissions, call
    a provider, publish bindings or resume workloads. Activation remains
    disabled. Idempotency replay returns the original response; poll GET
    for latest progress. Cancelling or cancelled cutovers conflict.

    Args:
        id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCutoverPreflight | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> ManagedPostgresCutoverPreflight | Problem | None:
    """Queue a restore activation preflight

     Requires managed PostgreSQL manage scope and verified email. Durably
    queues catalog inspection, including while provider provisioning is
    disabled. Concurrent pending requests keep the revision; requesting a
    completed check starts a new revision. Does not fence admissions, call
    a provider, publish bindings or resume workloads. Activation remains
    disabled. Idempotency replay returns the original response; poll GET
    for latest progress. Cancelling or cancelled cutovers conflict.

    Args:
        id (UUID):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresCutoverPreflight | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            idempotency_key=idempotency_key,
        )
    ).parsed
