from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.durable_entity_backup import DurableEntityBackup
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
    backup_id: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["namespace"] = namespace

    params["key"] = key

    params["environment"] = environment

    params["platform_tenant_id"] = platform_tenant_id

    params["backup_id"] = backup_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/entities/backups/get".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DurableEntityBackup | Problem | None:
    if response.status_code == 200:
        response_200 = DurableEntityBackup.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DurableEntityBackup | Problem]:
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
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
    backup_id: str,
) -> Response[DurableEntityBackup | Problem]:
    """Read one private application-state backup.

     Owner diagnostic preview requiring apps:read or admin and MFA where
    applicable. Requires durable entity app enablement. Private, no-store.
    No ownership acquisition, guest execution or writes. Backup listing is
    bounded and metadata-only; backup reads contain sensitive application data.
    Restore preview reports observed versions, recognized schema envelopes and
    preserved pending work. Compatibility is always unverified; schema equality
    does not validate application data. Preview grants no restore authority.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (str | Unset):
        backup_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DurableEntityBackup | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        namespace=namespace,
        key=key,
        environment=environment,
        platform_tenant_id=platform_tenant_id,
        backup_id=backup_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
    backup_id: str,
) -> DurableEntityBackup | Problem | None:
    """Read one private application-state backup.

     Owner diagnostic preview requiring apps:read or admin and MFA where
    applicable. Requires durable entity app enablement. Private, no-store.
    No ownership acquisition, guest execution or writes. Backup listing is
    bounded and metadata-only; backup reads contain sensitive application data.
    Restore preview reports observed versions, recognized schema envelopes and
    preserved pending work. Compatibility is always unverified; schema equality
    does not validate application data. Preview grants no restore authority.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (str | Unset):
        backup_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DurableEntityBackup | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        namespace=namespace,
        key=key,
        environment=environment,
        platform_tenant_id=platform_tenant_id,
        backup_id=backup_id,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
    backup_id: str,
) -> Response[DurableEntityBackup | Problem]:
    """Read one private application-state backup.

     Owner diagnostic preview requiring apps:read or admin and MFA where
    applicable. Requires durable entity app enablement. Private, no-store.
    No ownership acquisition, guest execution or writes. Backup listing is
    bounded and metadata-only; backup reads contain sensitive application data.
    Restore preview reports observed versions, recognized schema envelopes and
    preserved pending work. Compatibility is always unverified; schema equality
    does not validate application data. Preview grants no restore authority.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (str | Unset):
        backup_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DurableEntityBackup | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        namespace=namespace,
        key=key,
        environment=environment,
        platform_tenant_id=platform_tenant_id,
        backup_id=backup_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
    backup_id: str,
) -> DurableEntityBackup | Problem | None:
    """Read one private application-state backup.

     Owner diagnostic preview requiring apps:read or admin and MFA where
    applicable. Requires durable entity app enablement. Private, no-store.
    No ownership acquisition, guest execution or writes. Backup listing is
    bounded and metadata-only; backup reads contain sensitive application data.
    Restore preview reports observed versions, recognized schema envelopes and
    preserved pending work. Compatibility is always unverified; schema equality
    does not validate application data. Preview grants no restore authority.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (str | Unset):
        backup_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DurableEntityBackup | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            namespace=namespace,
            key=key,
            environment=environment,
            platform_tenant_id=platform_tenant_id,
            backup_id=backup_id,
        )
    ).parsed
