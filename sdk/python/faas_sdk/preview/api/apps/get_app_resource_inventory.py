from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_resource_inventory_response import AppResourceInventoryResponse
from ...models.get_app_resource_inventory_kind import GetAppResourceInventoryKind
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    kind: GetAppResourceInventoryKind,
    environment: str | Unset = UNSET,
    limit: int | Unset = 20,
    offset: int | Unset = 0,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_kind: str = kind
    params["kind"] = json_kind

    params["environment"] = environment

    params["limit"] = limit

    params["offset"] = offset

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/resource-inventory".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppResourceInventoryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppResourceInventoryResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
) -> Response[AppResourceInventoryResponse | Problem]:
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
    kind: GetAppResourceInventoryKind,
    environment: str | Unset = UNSET,
    limit: int | Unset = 20,
    offset: int | Unset = 0,
) -> Response[AppResourceInventoryResponse | Problem]:
    """Read a bounded page of app resource metadata.

     Requires apps:read and current application access. App-bound readers may
    inspect only their immutable app. This is metadata access: object data,
    database connections, credentials and lifecycle operations retain their
    own permissions. Counts and offsets are filtered by app, kind and exact
    scope in the native catalog before pagination. Resource capability flags
    still apply. Domains and crons are app-wide; omit environment for them.
    Pages are individual read snapshots; concurrent changes may move offsets.
    Metadata state does not establish runtime readiness or connectivity.
    No provider calls, probes, reservations or writes are performed.

    Args:
        slug (str):
        kind (GetAppResourceInventoryKind):
        environment (str | Unset):
        limit (int | Unset):  Default: 20.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppResourceInventoryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        kind=kind,
        environment=environment,
        limit=limit,
        offset=offset,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    kind: GetAppResourceInventoryKind,
    environment: str | Unset = UNSET,
    limit: int | Unset = 20,
    offset: int | Unset = 0,
) -> AppResourceInventoryResponse | Problem | None:
    """Read a bounded page of app resource metadata.

     Requires apps:read and current application access. App-bound readers may
    inspect only their immutable app. This is metadata access: object data,
    database connections, credentials and lifecycle operations retain their
    own permissions. Counts and offsets are filtered by app, kind and exact
    scope in the native catalog before pagination. Resource capability flags
    still apply. Domains and crons are app-wide; omit environment for them.
    Pages are individual read snapshots; concurrent changes may move offsets.
    Metadata state does not establish runtime readiness or connectivity.
    No provider calls, probes, reservations or writes are performed.

    Args:
        slug (str):
        kind (GetAppResourceInventoryKind):
        environment (str | Unset):
        limit (int | Unset):  Default: 20.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppResourceInventoryResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        kind=kind,
        environment=environment,
        limit=limit,
        offset=offset,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    kind: GetAppResourceInventoryKind,
    environment: str | Unset = UNSET,
    limit: int | Unset = 20,
    offset: int | Unset = 0,
) -> Response[AppResourceInventoryResponse | Problem]:
    """Read a bounded page of app resource metadata.

     Requires apps:read and current application access. App-bound readers may
    inspect only their immutable app. This is metadata access: object data,
    database connections, credentials and lifecycle operations retain their
    own permissions. Counts and offsets are filtered by app, kind and exact
    scope in the native catalog before pagination. Resource capability flags
    still apply. Domains and crons are app-wide; omit environment for them.
    Pages are individual read snapshots; concurrent changes may move offsets.
    Metadata state does not establish runtime readiness or connectivity.
    No provider calls, probes, reservations or writes are performed.

    Args:
        slug (str):
        kind (GetAppResourceInventoryKind):
        environment (str | Unset):
        limit (int | Unset):  Default: 20.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppResourceInventoryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        kind=kind,
        environment=environment,
        limit=limit,
        offset=offset,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    kind: GetAppResourceInventoryKind,
    environment: str | Unset = UNSET,
    limit: int | Unset = 20,
    offset: int | Unset = 0,
) -> AppResourceInventoryResponse | Problem | None:
    """Read a bounded page of app resource metadata.

     Requires apps:read and current application access. App-bound readers may
    inspect only their immutable app. This is metadata access: object data,
    database connections, credentials and lifecycle operations retain their
    own permissions. Counts and offsets are filtered by app, kind and exact
    scope in the native catalog before pagination. Resource capability flags
    still apply. Domains and crons are app-wide; omit environment for them.
    Pages are individual read snapshots; concurrent changes may move offsets.
    Metadata state does not establish runtime readiness or connectivity.
    No provider calls, probes, reservations or writes are performed.

    Args:
        slug (str):
        kind (GetAppResourceInventoryKind):
        environment (str | Unset):
        limit (int | Unset):  Default: 20.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppResourceInventoryResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            kind=kind,
            environment=environment,
            limit=limit,
            offset=offset,
        )
    ).parsed
