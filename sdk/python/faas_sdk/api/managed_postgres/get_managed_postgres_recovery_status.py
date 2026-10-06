from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_recovery_status import ManagedPostgresRecoveryStatus
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/postgres/databases/{id}/recovery".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresRecoveryStatus | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresRecoveryStatus.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresRecoveryStatus | Problem]:
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
) -> Response[ManagedPostgresRecoveryStatus | Problem]:
    """Read live managed PostgreSQL recovery limits

     Requires PostgreSQL read scope and MFA for interactive sessions. Reads
    metadata for the pinned source without connecting to SQL or waking
    compute. limits_known reports necessary limits, not guaranteed recovery
    of every timestamp. Missing or uncertain evidence is reported explicitly.
    Available independently of new-provisioning admission.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresRecoveryStatus | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresRecoveryStatus | Problem | None:
    """Read live managed PostgreSQL recovery limits

     Requires PostgreSQL read scope and MFA for interactive sessions. Reads
    metadata for the pinned source without connecting to SQL or waking
    compute. limits_known reports necessary limits, not guaranteed recovery
    of every timestamp. Missing or uncertain evidence is reported explicitly.
    Available independently of new-provisioning admission.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresRecoveryStatus | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedPostgresRecoveryStatus | Problem]:
    """Read live managed PostgreSQL recovery limits

     Requires PostgreSQL read scope and MFA for interactive sessions. Reads
    metadata for the pinned source without connecting to SQL or waking
    compute. limits_known reports necessary limits, not guaranteed recovery
    of every timestamp. Missing or uncertain evidence is reported explicitly.
    Available independently of new-provisioning admission.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresRecoveryStatus | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedPostgresRecoveryStatus | Problem | None:
    """Read live managed PostgreSQL recovery limits

     Requires PostgreSQL read scope and MFA for interactive sessions. Reads
    metadata for the pinned source without connecting to SQL or waking
    compute. limits_known reports necessary limits, not guaranteed recovery
    of every timestamp. Missing or uncertain evidence is reported explicitly.
    Available independently of new-provisioning admission.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresRecoveryStatus | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
