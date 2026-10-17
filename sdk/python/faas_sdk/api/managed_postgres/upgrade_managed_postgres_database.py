from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_major_upgrade import ManagedPostgresMajorUpgrade
from ...models.problem import Problem
from ...models.upgrade_managed_postgres_database_request import UpgradeManagedPostgresDatabaseRequest
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: UpgradeManagedPostgresDatabaseRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/postgres/databases/{id}/upgrade".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresMajorUpgrade | Problem:
    if response.status_code == 202:
        response_202 = ManagedPostgresMajorUpgrade.from_dict(response.json())

        return response_202

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresMajorUpgrade | Problem]:
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
    body: UpgradeManagedPostgresDatabaseRequest,
) -> Response[ManagedPostgresMajorUpgrade | Problem]:
    """Upgrade managed PostgreSQL to a later major

     Durably reserves an in-place upgrade of the database to a later PostgreSQL major offered by its
    backend (capabilities major_upgrade). Nothing else changes. The database stops for the upgrade and
    starts on the new major, so clients disconnect once; planner statistics are rebuilt after it serves
    again. The request_id UUID is the durable idempotency key; repeat the same UUID and major to recover
    progress, and a request never upgrades twice. Another resize, deletion, binding change, restore or
    cutover conflicts while updating. An upgrade can fail for good. With upgrade_refused, PostgreSQL
    refused to upgrade the cluster, which stays on its major unchanged; remove what it reported and
    request a new upgrade. With upgrade_failed, the database needs a point-in-time restore to before the
    upgrade, which recovers on the old major. Points in time before an upgrade restore on the major the
    database ran then.

    Args:
        id (str):
        body (UpgradeManagedPostgresDatabaseRequest): Canonical UUID request for an in-place
            upgrade of an existing managed PostgreSQL database to a later major.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresMajorUpgrade | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpgradeManagedPostgresDatabaseRequest,
) -> ManagedPostgresMajorUpgrade | Problem | None:
    """Upgrade managed PostgreSQL to a later major

     Durably reserves an in-place upgrade of the database to a later PostgreSQL major offered by its
    backend (capabilities major_upgrade). Nothing else changes. The database stops for the upgrade and
    starts on the new major, so clients disconnect once; planner statistics are rebuilt after it serves
    again. The request_id UUID is the durable idempotency key; repeat the same UUID and major to recover
    progress, and a request never upgrades twice. Another resize, deletion, binding change, restore or
    cutover conflicts while updating. An upgrade can fail for good. With upgrade_refused, PostgreSQL
    refused to upgrade the cluster, which stays on its major unchanged; remove what it reported and
    request a new upgrade. With upgrade_failed, the database needs a point-in-time restore to before the
    upgrade, which recovers on the old major. Points in time before an upgrade restore on the major the
    database ran then.

    Args:
        id (str):
        body (UpgradeManagedPostgresDatabaseRequest): Canonical UUID request for an in-place
            upgrade of an existing managed PostgreSQL database to a later major.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresMajorUpgrade | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpgradeManagedPostgresDatabaseRequest,
) -> Response[ManagedPostgresMajorUpgrade | Problem]:
    """Upgrade managed PostgreSQL to a later major

     Durably reserves an in-place upgrade of the database to a later PostgreSQL major offered by its
    backend (capabilities major_upgrade). Nothing else changes. The database stops for the upgrade and
    starts on the new major, so clients disconnect once; planner statistics are rebuilt after it serves
    again. The request_id UUID is the durable idempotency key; repeat the same UUID and major to recover
    progress, and a request never upgrades twice. Another resize, deletion, binding change, restore or
    cutover conflicts while updating. An upgrade can fail for good. With upgrade_refused, PostgreSQL
    refused to upgrade the cluster, which stays on its major unchanged; remove what it reported and
    request a new upgrade. With upgrade_failed, the database needs a point-in-time restore to before the
    upgrade, which recovers on the old major. Points in time before an upgrade restore on the major the
    database ran then.

    Args:
        id (str):
        body (UpgradeManagedPostgresDatabaseRequest): Canonical UUID request for an in-place
            upgrade of an existing managed PostgreSQL database to a later major.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresMajorUpgrade | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpgradeManagedPostgresDatabaseRequest,
) -> ManagedPostgresMajorUpgrade | Problem | None:
    """Upgrade managed PostgreSQL to a later major

     Durably reserves an in-place upgrade of the database to a later PostgreSQL major offered by its
    backend (capabilities major_upgrade). Nothing else changes. The database stops for the upgrade and
    starts on the new major, so clients disconnect once; planner statistics are rebuilt after it serves
    again. The request_id UUID is the durable idempotency key; repeat the same UUID and major to recover
    progress, and a request never upgrades twice. Another resize, deletion, binding change, restore or
    cutover conflicts while updating. An upgrade can fail for good. With upgrade_refused, PostgreSQL
    refused to upgrade the cluster, which stays on its major unchanged; remove what it reported and
    request a new upgrade. With upgrade_failed, the database needs a point-in-time restore to before the
    upgrade, which recovers on the old major. Points in time before an upgrade restore on the major the
    database ran then.

    Args:
        id (str):
        body (UpgradeManagedPostgresDatabaseRequest): Canonical UUID request for an in-place
            upgrade of an existing managed PostgreSQL database to a later major.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresMajorUpgrade | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
