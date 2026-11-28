from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_platform_tenant_self_operations_state import (
    ListPlatformTenantSelfOperationsState,
)
from ...models.operation_list_response import OperationListResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app_id: UUID,
    scope: str,
    name: str | Unset = UNSET,
    subject_type: str | Unset = UNSET,
    subject_id: str | Unset = UNSET,
    state: ListPlatformTenantSelfOperationsState | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params["scope"] = scope

    params["name"] = name

    params["subject_type"] = subject_type

    params["subject_id"] = subject_id

    json_state: str | Unset = UNSET
    if not isinstance(state, Unset):
        json_state = state

    params["state"] = json_state

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/customer-operations",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationListResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperationListResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    name: str | Unset = UNSET,
    subject_type: str | Unset = UNSET,
    subject_id: str | Unset = UNSET,
    state: ListPlatformTenantSelfOperationsState | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[OperationListResponse | Problem]:
    """Discover and reopen this customer's retained work.

     Requires platform_tenant:operations:read and an explicit app_id and environment scope. Identity
    comes only from the authenticated tenant token. Pages contain status summaries, never source input,
    result bytes, artifact locations or execution authority. Active work remains visible; expired
    settled work is omitted. Creation time and ID determine descending order. An opaque cursor is bound
    to the account, tenant, app, scope and filters. This is a live view, not a transaction snapshot:
    state filters and retention can change membership. Closed preview admission does not disable history
    reads.

    Args:
        app_id (UUID):
        scope (str):
        name (str | Unset):
        subject_type (str | Unset):
        subject_id (str | Unset):
        state (ListPlatformTenantSelfOperationsState | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationListResponse | Problem]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        name=name,
        subject_type=subject_type,
        subject_id=subject_id,
        state=state,
        limit=limit,
        cursor=cursor,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    name: str | Unset = UNSET,
    subject_type: str | Unset = UNSET,
    subject_id: str | Unset = UNSET,
    state: ListPlatformTenantSelfOperationsState | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> OperationListResponse | Problem | None:
    """Discover and reopen this customer's retained work.

     Requires platform_tenant:operations:read and an explicit app_id and environment scope. Identity
    comes only from the authenticated tenant token. Pages contain status summaries, never source input,
    result bytes, artifact locations or execution authority. Active work remains visible; expired
    settled work is omitted. Creation time and ID determine descending order. An opaque cursor is bound
    to the account, tenant, app, scope and filters. This is a live view, not a transaction snapshot:
    state filters and retention can change membership. Closed preview admission does not disable history
    reads.

    Args:
        app_id (UUID):
        scope (str):
        name (str | Unset):
        subject_type (str | Unset):
        subject_id (str | Unset):
        state (ListPlatformTenantSelfOperationsState | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationListResponse | Problem
    """

    return sync_detailed(
        client=client,
        app_id=app_id,
        scope=scope,
        name=name,
        subject_type=subject_type,
        subject_id=subject_id,
        state=state,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    name: str | Unset = UNSET,
    subject_type: str | Unset = UNSET,
    subject_id: str | Unset = UNSET,
    state: ListPlatformTenantSelfOperationsState | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[OperationListResponse | Problem]:
    """Discover and reopen this customer's retained work.

     Requires platform_tenant:operations:read and an explicit app_id and environment scope. Identity
    comes only from the authenticated tenant token. Pages contain status summaries, never source input,
    result bytes, artifact locations or execution authority. Active work remains visible; expired
    settled work is omitted. Creation time and ID determine descending order. An opaque cursor is bound
    to the account, tenant, app, scope and filters. This is a live view, not a transaction snapshot:
    state filters and retention can change membership. Closed preview admission does not disable history
    reads.

    Args:
        app_id (UUID):
        scope (str):
        name (str | Unset):
        subject_type (str | Unset):
        subject_id (str | Unset):
        state (ListPlatformTenantSelfOperationsState | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationListResponse | Problem]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
        scope=scope,
        name=name,
        subject_type=subject_type,
        subject_id=subject_id,
        state=state,
        limit=limit,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    app_id: UUID,
    scope: str,
    name: str | Unset = UNSET,
    subject_type: str | Unset = UNSET,
    subject_id: str | Unset = UNSET,
    state: ListPlatformTenantSelfOperationsState | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> OperationListResponse | Problem | None:
    """Discover and reopen this customer's retained work.

     Requires platform_tenant:operations:read and an explicit app_id and environment scope. Identity
    comes only from the authenticated tenant token. Pages contain status summaries, never source input,
    result bytes, artifact locations or execution authority. Active work remains visible; expired
    settled work is omitted. Creation time and ID determine descending order. An opaque cursor is bound
    to the account, tenant, app, scope and filters. This is a live view, not a transaction snapshot:
    state filters and retention can change membership. Closed preview admission does not disable history
    reads.

    Args:
        app_id (UUID):
        scope (str):
        name (str | Unset):
        subject_type (str | Unset):
        subject_id (str | Unset):
        state (ListPlatformTenantSelfOperationsState | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationListResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            app_id=app_id,
            scope=scope,
            name=name,
            subject_type=subject_type,
            subject_id=subject_id,
            state=state,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
