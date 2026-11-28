from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_cutover import ManagedPostgresCutover
from ...models.prepare_managed_postgres_cutover_request import PrepareManagedPostgresCutoverRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: PrepareManagedPostgresCutoverRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/postgres/cutovers",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresCutover | Problem:
    if response.status_code == 202:
        response_202 = ManagedPostgresCutover.from_dict(response.json())

        return response_202

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresCutover | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PrepareManagedPostgresCutoverRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedPostgresCutover | Problem]:
    """Stage a managed PostgreSQL restore cutover

     Requires managed PostgreSQL manage scope and a verified email. Stages all
    source bindings for one app and scope on a ready restore target. Pins both
    databases and the source bindings. Credentials remain unpublished;
    workloads continue using the source. No activation is performed.

    Args:
        idempotency_key (str | Unset):
        body (PrepareManagedPostgresCutoverRequest): Stage source bindings for one app and scope
            on a ready database restored from that source.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCutover | Problem]
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
    body: PrepareManagedPostgresCutoverRequest,
    idempotency_key: str | Unset = UNSET,
) -> ManagedPostgresCutover | Problem | None:
    """Stage a managed PostgreSQL restore cutover

     Requires managed PostgreSQL manage scope and a verified email. Stages all
    source bindings for one app and scope on a ready restore target. Pins both
    databases and the source bindings. Credentials remain unpublished;
    workloads continue using the source. No activation is performed.

    Args:
        idempotency_key (str | Unset):
        body (PrepareManagedPostgresCutoverRequest): Stage source bindings for one app and scope
            on a ready database restored from that source.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresCutover | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PrepareManagedPostgresCutoverRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedPostgresCutover | Problem]:
    """Stage a managed PostgreSQL restore cutover

     Requires managed PostgreSQL manage scope and a verified email. Stages all
    source bindings for one app and scope on a ready restore target. Pins both
    databases and the source bindings. Credentials remain unpublished;
    workloads continue using the source. No activation is performed.

    Args:
        idempotency_key (str | Unset):
        body (PrepareManagedPostgresCutoverRequest): Stage source bindings for one app and scope
            on a ready database restored from that source.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresCutover | Problem]
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
    body: PrepareManagedPostgresCutoverRequest,
    idempotency_key: str | Unset = UNSET,
) -> ManagedPostgresCutover | Problem | None:
    """Stage a managed PostgreSQL restore cutover

     Requires managed PostgreSQL manage scope and a verified email. Stages all
    source bindings for one app and scope on a ready restore target. Pins both
    databases and the source bindings. Credentials remain unpublished;
    workloads continue using the source. No activation is performed.

    Args:
        idempotency_key (str | Unset):
        body (PrepareManagedPostgresCutoverRequest): Stage source bindings for one app and scope
            on a ready database restored from that source.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresCutover | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
