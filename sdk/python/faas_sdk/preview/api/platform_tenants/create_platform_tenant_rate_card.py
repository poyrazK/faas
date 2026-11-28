from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_platform_tenant_rate_card_request import CreatePlatformTenantRateCardRequest
from ...models.platform_tenant_rate_card_response import PlatformTenantRateCardResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    body: CreatePlatformTenantRateCardRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/platform-tenants/{id}/rate-cards".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantRateCardResponse | Problem | None:
    if response.status_code == 201:
        response_201 = PlatformTenantRateCardResponse.from_dict(response.json())

        return response_201

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantRateCardResponse | Problem]:
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
    body: CreatePlatformTenantRateCardRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[PlatformTenantRateCardResponse | Problem]:
    """Set an immutable cross-app customer price.

     Appends a version that overrides app-level prices for this tenant from its effective UTC minute.
    Before the first tenant version takes effect, statement pricing falls back to each app's rate card.
    A tenant can use only one currency; finalized statement revisions are never rewritten.

    Args:
        id (UUID):
        idempotency_key (str | Unset):
        body (CreatePlatformTenantRateCardRequest): Immutable tenant-wide price per attributed
            request unit. Currency defaults to EUR and effective_from defaults to the next UTC minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantRateCardResponse | Problem]
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
    body: CreatePlatformTenantRateCardRequest,
    idempotency_key: str | Unset = UNSET,
) -> PlatformTenantRateCardResponse | Problem | None:
    """Set an immutable cross-app customer price.

     Appends a version that overrides app-level prices for this tenant from its effective UTC minute.
    Before the first tenant version takes effect, statement pricing falls back to each app's rate card.
    A tenant can use only one currency; finalized statement revisions are never rewritten.

    Args:
        id (UUID):
        idempotency_key (str | Unset):
        body (CreatePlatformTenantRateCardRequest): Immutable tenant-wide price per attributed
            request unit. Currency defaults to EUR and effective_from defaults to the next UTC minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantRateCardResponse | Problem
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
    body: CreatePlatformTenantRateCardRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[PlatformTenantRateCardResponse | Problem]:
    """Set an immutable cross-app customer price.

     Appends a version that overrides app-level prices for this tenant from its effective UTC minute.
    Before the first tenant version takes effect, statement pricing falls back to each app's rate card.
    A tenant can use only one currency; finalized statement revisions are never rewritten.

    Args:
        id (UUID):
        idempotency_key (str | Unset):
        body (CreatePlatformTenantRateCardRequest): Immutable tenant-wide price per attributed
            request unit. Currency defaults to EUR and effective_from defaults to the next UTC minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantRateCardResponse | Problem]
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
    body: CreatePlatformTenantRateCardRequest,
    idempotency_key: str | Unset = UNSET,
) -> PlatformTenantRateCardResponse | Problem | None:
    """Set an immutable cross-app customer price.

     Appends a version that overrides app-level prices for this tenant from its effective UTC minute.
    Before the first tenant version takes effect, statement pricing falls back to each app's rate card.
    A tenant can use only one currency; finalized statement revisions are never rewritten.

    Args:
        id (UUID):
        idempotency_key (str | Unset):
        body (CreatePlatformTenantRateCardRequest): Immutable tenant-wide price per attributed
            request unit. Currency defaults to EUR and effective_from defaults to the next UTC minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantRateCardResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
