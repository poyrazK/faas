import datetime
from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_usage_response import PlatformTenantUsageResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    json_until: str | Unset = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/usage",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantUsageResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantUsageResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantUsageResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> Response[PlatformTenantUsageResponse | Problem]:
    """Read this tenant's own cross-app raw usage.

     Requires a tenant-bound access token with platform_tenant:usage:read. The credential cannot select
    or impersonate a different tenant.

    Args:
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantUsageResponse | Problem]
    """

    kwargs = _get_kwargs(
        since=since,
        until=until,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> PlatformTenantUsageResponse | Problem | None:
    """Read this tenant's own cross-app raw usage.

     Requires a tenant-bound access token with platform_tenant:usage:read. The credential cannot select
    or impersonate a different tenant.

    Args:
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantUsageResponse | Problem
    """

    return sync_detailed(
        client=client,
        since=since,
        until=until,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> Response[PlatformTenantUsageResponse | Problem]:
    """Read this tenant's own cross-app raw usage.

     Requires a tenant-bound access token with platform_tenant:usage:read. The credential cannot select
    or impersonate a different tenant.

    Args:
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantUsageResponse | Problem]
    """

    kwargs = _get_kwargs(
        since=since,
        until=until,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> PlatformTenantUsageResponse | Problem | None:
    """Read this tenant's own cross-app raw usage.

     Requires a tenant-bound access token with platform_tenant:usage:read. The credential cannot select
    or impersonate a different tenant.

    Args:
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantUsageResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            since=since,
            until=until,
        )
    ).parsed
