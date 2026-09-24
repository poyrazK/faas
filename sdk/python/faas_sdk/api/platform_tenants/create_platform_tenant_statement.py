from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_api_consumer_usage_statement_request import CreateAPIConsumerUsageStatementRequest
from ...models.platform_tenant_statement_response import PlatformTenantStatementResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    body: CreateAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenants/{id}/usage-statements".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantStatementResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantStatementResponse.from_dict(response.json())

        return response_200

    if response.status_code == 201:
        response_201 = PlatformTenantStatementResponse.from_dict(response.json())

        return response_201

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
) -> Response[PlatformTenantStatementResponse | Problem]:
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
    body: CreateAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[PlatformTenantStatementResponse | Problem]:
    """Snapshot tenant-attributed usage across apps or create a late-usage adjustment.

     A draft replays unchanged. After finalization, new units create the next revision; no new units
    replay the latest revision. Mixed currencies are rejected.

    Args:
        id (UUID):
        idempotency_key (str | Unset):
        body (CreateAPIConsumerUsageStatementRequest): Explicit UTC-minute period to snapshot as
            an immutable usage statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantStatementResponse | Problem]
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
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> PlatformTenantStatementResponse | Problem | None:
    """Snapshot tenant-attributed usage across apps or create a late-usage adjustment.

     A draft replays unchanged. After finalization, new units create the next revision; no new units
    replay the latest revision. Mixed currencies are rejected.

    Args:
        id (UUID):
        idempotency_key (str | Unset):
        body (CreateAPIConsumerUsageStatementRequest): Explicit UTC-minute period to snapshot as
            an immutable usage statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantStatementResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[PlatformTenantStatementResponse | Problem]:
    """Snapshot tenant-attributed usage across apps or create a late-usage adjustment.

     A draft replays unchanged. After finalization, new units create the next revision; no new units
    replay the latest revision. Mixed currencies are rejected.

    Args:
        id (UUID):
        idempotency_key (str | Unset):
        body (CreateAPIConsumerUsageStatementRequest): Explicit UTC-minute period to snapshot as
            an immutable usage statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantStatementResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerUsageStatementRequest,
    idempotency_key: str | Unset = UNSET,
) -> PlatformTenantStatementResponse | Problem | None:
    """Snapshot tenant-attributed usage across apps or create a late-usage adjustment.

     A draft replays unchanged. After finalization, new units create the next revision; no new units
    replay the latest revision. Mixed currencies are rejected.

    Args:
        id (UUID):
        idempotency_key (str | Unset):
        body (CreateAPIConsumerUsageStatementRequest): Explicit UTC-minute period to snapshot as
            an immutable usage statement.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantStatementResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
