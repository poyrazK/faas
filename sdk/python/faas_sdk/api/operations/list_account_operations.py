from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_account_operations_state import ListAccountOperationsState
from ...models.operation_list_response import OperationListResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    name: str | Unset = UNSET,
    state: ListAccountOperationsState | Unset = UNSET,
    subject_type: str | Unset = UNSET,
    subject_id: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    json_tenant_id: str | Unset = UNSET
    if not isinstance(tenant_id, Unset):
        json_tenant_id = str(tenant_id)
    params["tenant_id"] = json_tenant_id

    params["name"] = name

    json_state: str | Unset = UNSET
    if not isinstance(state, Unset):
        json_state = state

    params["state"] = json_state

    params["subject_type"] = subject_type

    params["subject_id"] = subject_id

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/operations".format(
            slug=quote(str(slug), safe=""),
        ),
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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

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
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    name: str | Unset = UNSET,
    state: ListAccountOperationsState | Unset = UNSET,
    subject_type: str | Unset = UNSET,
    subject_id: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[OperationListResponse | Problem]:
    """List retained customer operations for an account-owned app.

     Requires account read scope and MFA. Explicit scope and optional tenant filter select data within
    account ownership. Descending keyset pages exclude private inputs and results. Customer tokens
    cannot access this operator route.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):
        name (str | Unset):
        state (ListAccountOperationsState | Unset):
        subject_type (str | Unset):
        subject_id (str | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        tenant_id=tenant_id,
        name=name,
        state=state,
        subject_type=subject_type,
        subject_id=subject_id,
        limit=limit,
        cursor=cursor,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    name: str | Unset = UNSET,
    state: ListAccountOperationsState | Unset = UNSET,
    subject_type: str | Unset = UNSET,
    subject_id: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> OperationListResponse | Problem | None:
    """List retained customer operations for an account-owned app.

     Requires account read scope and MFA. Explicit scope and optional tenant filter select data within
    account ownership. Descending keyset pages exclude private inputs and results. Customer tokens
    cannot access this operator route.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):
        name (str | Unset):
        state (ListAccountOperationsState | Unset):
        subject_type (str | Unset):
        subject_id (str | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationListResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
        tenant_id=tenant_id,
        name=name,
        state=state,
        subject_type=subject_type,
        subject_id=subject_id,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    name: str | Unset = UNSET,
    state: ListAccountOperationsState | Unset = UNSET,
    subject_type: str | Unset = UNSET,
    subject_id: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[OperationListResponse | Problem]:
    """List retained customer operations for an account-owned app.

     Requires account read scope and MFA. Explicit scope and optional tenant filter select data within
    account ownership. Descending keyset pages exclude private inputs and results. Customer tokens
    cannot access this operator route.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):
        name (str | Unset):
        state (ListAccountOperationsState | Unset):
        subject_type (str | Unset):
        subject_id (str | Unset):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        tenant_id=tenant_id,
        name=name,
        state=state,
        subject_type=subject_type,
        subject_id=subject_id,
        limit=limit,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    name: str | Unset = UNSET,
    state: ListAccountOperationsState | Unset = UNSET,
    subject_type: str | Unset = UNSET,
    subject_id: str | Unset = UNSET,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> OperationListResponse | Problem | None:
    """List retained customer operations for an account-owned app.

     Requires account read scope and MFA. Explicit scope and optional tenant filter select data within
    account ownership. Descending keyset pages exclude private inputs and results. Customer tokens
    cannot access this operator route.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):
        name (str | Unset):
        state (ListAccountOperationsState | Unset):
        subject_type (str | Unset):
        subject_id (str | Unset):
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
            slug=slug,
            client=client,
            scope=scope,
            tenant_id=tenant_id,
            name=name,
            state=state,
            subject_type=subject_type,
            subject_id=subject_id,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
