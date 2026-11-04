from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_self_activation_response import PlatformTenantSelfActivationResponse
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/activation",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantSelfActivationResponse | None:
    if response.status_code == 200:
        response_200 = PlatformTenantSelfActivationResponse.from_dict(response.json())

        return response_200

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantSelfActivationResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[PlatformTenantSelfActivationResponse]:
    """Read this tenant's own redacted activation snapshot.

     Requires a tenant-bound access token with platform_tenant:activation:read. The tenant is derived
    from the bearer; callers cannot select another tenant. The snapshot includes only safe latest-
    deployment status for linked surfaces; raw DNS, certificate, and deployment errors, DNS challenge
    tokens, app IDs, deployment IDs, and source metadata are omitted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantSelfActivationResponse]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> PlatformTenantSelfActivationResponse | None:
    """Read this tenant's own redacted activation snapshot.

     Requires a tenant-bound access token with platform_tenant:activation:read. The tenant is derived
    from the bearer; callers cannot select another tenant. The snapshot includes only safe latest-
    deployment status for linked surfaces; raw DNS, certificate, and deployment errors, DNS challenge
    tokens, app IDs, deployment IDs, and source metadata are omitted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantSelfActivationResponse
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[PlatformTenantSelfActivationResponse]:
    """Read this tenant's own redacted activation snapshot.

     Requires a tenant-bound access token with platform_tenant:activation:read. The tenant is derived
    from the bearer; callers cannot select another tenant. The snapshot includes only safe latest-
    deployment status for linked surfaces; raw DNS, certificate, and deployment errors, DNS challenge
    tokens, app IDs, deployment IDs, and source metadata are omitted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantSelfActivationResponse]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> PlatformTenantSelfActivationResponse | None:
    """Read this tenant's own redacted activation snapshot.

     Requires a tenant-bound access token with platform_tenant:activation:read. The tenant is derived
    from the bearer; callers cannot select another tenant. The snapshot includes only safe latest-
    deployment status for linked surfaces; raw DNS, certificate, and deployment errors, DNS challenge
    tokens, app IDs, deployment IDs, and source metadata are omitted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantSelfActivationResponse
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
