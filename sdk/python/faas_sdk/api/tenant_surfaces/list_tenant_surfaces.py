from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_tenant_surfaces_response import ListTenantSurfacesResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/tenant-surfaces".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListTenantSurfacesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListTenantSurfacesResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ListTenantSurfacesResponse | Problem]:
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
) -> Response[ListTenantSurfacesResponse | Problem]:
    """List tenant surfaces on an app.

     Returns every active tenant surface on the app. Soft-deleted
    surfaces are filtered out server-side. Returns 503 when the
    `FAAS_TENANT_SURFACES_ENABLED` flag is off (the cluster ships
    dark until the cert-engine real-mint ADR lands).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListTenantSurfacesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> ListTenantSurfacesResponse | Problem | None:
    """List tenant surfaces on an app.

     Returns every active tenant surface on the app. Soft-deleted
    surfaces are filtered out server-side. Returns 503 when the
    `FAAS_TENANT_SURFACES_ENABLED` flag is off (the cluster ships
    dark until the cert-engine real-mint ADR lands).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListTenantSurfacesResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ListTenantSurfacesResponse | Problem]:
    """List tenant surfaces on an app.

     Returns every active tenant surface on the app. Soft-deleted
    surfaces are filtered out server-side. Returns 503 when the
    `FAAS_TENANT_SURFACES_ENABLED` flag is off (the cluster ships
    dark until the cert-engine real-mint ADR lands).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListTenantSurfacesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> ListTenantSurfacesResponse | Problem | None:
    """List tenant surfaces on an app.

     Returns every active tenant surface on the app. Soft-deleted
    surfaces are filtered out server-side. Returns 503 when the
    `FAAS_TENANT_SURFACES_ENABLED` flag is off (the cluster ships
    dark until the cert-engine real-mint ADR lands).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListTenantSurfacesResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
