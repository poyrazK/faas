from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_overage import ManagedPostgresOverage
from ...models.managed_postgres_overage_request import ManagedPostgresOverageRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: ManagedPostgresOverageRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/postgres/overage",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresOverage | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresOverage.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresOverage | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ManagedPostgresOverageRequest,
) -> Response[ManagedPostgresOverage | Problem]:
    """Turn compute overage on or off

     Paid plans only; Free stops at its allowance and answers 403 managed_postgres_not_in_plan. Turning
    overage off while past the allowance suspends the databases at the next cap sweep. Compute above the
    allowance is not billed until managed PostgreSQL has invoicing.

    Args:
        body (ManagedPostgresOverageRequest): Turn an account's managed PostgreSQL compute overage
            on or off.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresOverage | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: ManagedPostgresOverageRequest,
) -> ManagedPostgresOverage | Problem | None:
    """Turn compute overage on or off

     Paid plans only; Free stops at its allowance and answers 403 managed_postgres_not_in_plan. Turning
    overage off while past the allowance suspends the databases at the next cap sweep. Compute above the
    allowance is not billed until managed PostgreSQL has invoicing.

    Args:
        body (ManagedPostgresOverageRequest): Turn an account's managed PostgreSQL compute overage
            on or off.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresOverage | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ManagedPostgresOverageRequest,
) -> Response[ManagedPostgresOverage | Problem]:
    """Turn compute overage on or off

     Paid plans only; Free stops at its allowance and answers 403 managed_postgres_not_in_plan. Turning
    overage off while past the allowance suspends the databases at the next cap sweep. Compute above the
    allowance is not billed until managed PostgreSQL has invoicing.

    Args:
        body (ManagedPostgresOverageRequest): Turn an account's managed PostgreSQL compute overage
            on or off.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresOverage | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ManagedPostgresOverageRequest,
) -> ManagedPostgresOverage | Problem | None:
    """Turn compute overage on or off

     Paid plans only; Free stops at its allowance and answers 403 managed_postgres_not_in_plan. Turning
    overage off while past the allowance suspends the databases at the next cap sweep. Compute above the
    allowance is not billed until managed PostgreSQL has invoicing.

    Args:
        body (ManagedPostgresOverageRequest): Turn an account's managed PostgreSQL compute overage
            on or off.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresOverage | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
