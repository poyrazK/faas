from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_tenant_surface_request import CreateTenantSurfaceRequest
from ...models.problem import Problem
from ...models.tenant_surface_response import TenantSurfaceResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: CreateTenantSurfaceRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/tenant-surfaces".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | TenantSurfaceResponse | None:
    if response.status_code == 202:
        response_202 = TenantSurfaceResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | TenantSurfaceResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateTenantSurfaceRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | TenantSurfaceResponse]:
    """Add a tenant surface with seed hostnames.

     The customer-facing surface for issue #879 / ADR-100. One
    surface holds N hostnames under one SAN bundle. Returns 202
    (the cert engine has to mint; the surface is in pending/active
    state).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateTenantSurfaceRequest): Create a tenant surface with a seed set of hostnames.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TenantSurfaceResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateTenantSurfaceRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | TenantSurfaceResponse | None:
    """Add a tenant surface with seed hostnames.

     The customer-facing surface for issue #879 / ADR-100. One
    surface holds N hostnames under one SAN bundle. Returns 202
    (the cert engine has to mint; the surface is in pending/active
    state).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateTenantSurfaceRequest): Create a tenant surface with a seed set of hostnames.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TenantSurfaceResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateTenantSurfaceRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | TenantSurfaceResponse]:
    """Add a tenant surface with seed hostnames.

     The customer-facing surface for issue #879 / ADR-100. One
    surface holds N hostnames under one SAN bundle. Returns 202
    (the cert engine has to mint; the surface is in pending/active
    state).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateTenantSurfaceRequest): Create a tenant surface with a seed set of hostnames.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TenantSurfaceResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateTenantSurfaceRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | TenantSurfaceResponse | None:
    """Add a tenant surface with seed hostnames.

     The customer-facing surface for issue #879 / ADR-100. One
    surface holds N hostnames under one SAN bundle. Returns 202
    (the cert engine has to mint; the surface is in pending/active
    state).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateTenantSurfaceRequest): Create a tenant surface with a seed set of hostnames.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TenantSurfaceResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
