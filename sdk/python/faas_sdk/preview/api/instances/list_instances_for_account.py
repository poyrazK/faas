from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_instances_response import ListInstancesResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    before: UUID | Unset = UNSET,
    limit: int | Unset = 25,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_before: str | Unset = UNSET
    if not isinstance(before, Unset):
        json_before = str(before)
    params["before"] = json_before

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/instances",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListInstancesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListInstancesResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ListInstancesResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    before: UUID | Unset = UNSET,
    limit: int | Unset = 25,
) -> Response[ListInstancesResponse | Problem]:
    """List every live instance across the caller's account.

     Replaces the per-app fan-out from `/v1/apps/{slug}/instances`
    with one account-scoped read (issue #393). Each instance carries
    its `app_id`; cross-account isolation is enforced by the SQL
    `apps.account_id = $1` join (test: see
    `TestListInstancesForAccount_CrossAccountIsolation`).

    Cursor: `?before=<id>` (the instances.id UUIDv7). Defaults to 25
    per page; capped at 100; invalid `limit` returns 400
    `code_validation` with `limit=100` and the observed value
    (RFC 7807 strict mode, matching `/v1/invoices`).

    Rate-limit tiering: one call now replaces N per-app calls,
    so per-page-load token spend drops from N to 1. The
    per-account bucket (ADR-040) still applies at the gatewayd
    edge; this route charges 1 token via the apid authLimited
    middleware, same as every other `/v1/*` route.

    Args:
        before (UUID | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListInstancesResponse | Problem]
    """

    kwargs = _get_kwargs(
        before=before,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    before: UUID | Unset = UNSET,
    limit: int | Unset = 25,
) -> ListInstancesResponse | Problem | None:
    """List every live instance across the caller's account.

     Replaces the per-app fan-out from `/v1/apps/{slug}/instances`
    with one account-scoped read (issue #393). Each instance carries
    its `app_id`; cross-account isolation is enforced by the SQL
    `apps.account_id = $1` join (test: see
    `TestListInstancesForAccount_CrossAccountIsolation`).

    Cursor: `?before=<id>` (the instances.id UUIDv7). Defaults to 25
    per page; capped at 100; invalid `limit` returns 400
    `code_validation` with `limit=100` and the observed value
    (RFC 7807 strict mode, matching `/v1/invoices`).

    Rate-limit tiering: one call now replaces N per-app calls,
    so per-page-load token spend drops from N to 1. The
    per-account bucket (ADR-040) still applies at the gatewayd
    edge; this route charges 1 token via the apid authLimited
    middleware, same as every other `/v1/*` route.

    Args:
        before (UUID | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListInstancesResponse | Problem
    """

    return sync_detailed(
        client=client,
        before=before,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    before: UUID | Unset = UNSET,
    limit: int | Unset = 25,
) -> Response[ListInstancesResponse | Problem]:
    """List every live instance across the caller's account.

     Replaces the per-app fan-out from `/v1/apps/{slug}/instances`
    with one account-scoped read (issue #393). Each instance carries
    its `app_id`; cross-account isolation is enforced by the SQL
    `apps.account_id = $1` join (test: see
    `TestListInstancesForAccount_CrossAccountIsolation`).

    Cursor: `?before=<id>` (the instances.id UUIDv7). Defaults to 25
    per page; capped at 100; invalid `limit` returns 400
    `code_validation` with `limit=100` and the observed value
    (RFC 7807 strict mode, matching `/v1/invoices`).

    Rate-limit tiering: one call now replaces N per-app calls,
    so per-page-load token spend drops from N to 1. The
    per-account bucket (ADR-040) still applies at the gatewayd
    edge; this route charges 1 token via the apid authLimited
    middleware, same as every other `/v1/*` route.

    Args:
        before (UUID | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListInstancesResponse | Problem]
    """

    kwargs = _get_kwargs(
        before=before,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    before: UUID | Unset = UNSET,
    limit: int | Unset = 25,
) -> ListInstancesResponse | Problem | None:
    """List every live instance across the caller's account.

     Replaces the per-app fan-out from `/v1/apps/{slug}/instances`
    with one account-scoped read (issue #393). Each instance carries
    its `app_id`; cross-account isolation is enforced by the SQL
    `apps.account_id = $1` join (test: see
    `TestListInstancesForAccount_CrossAccountIsolation`).

    Cursor: `?before=<id>` (the instances.id UUIDv7). Defaults to 25
    per page; capped at 100; invalid `limit` returns 400
    `code_validation` with `limit=100` and the observed value
    (RFC 7807 strict mode, matching `/v1/invoices`).

    Rate-limit tiering: one call now replaces N per-app calls,
    so per-page-load token spend drops from N to 1. The
    per-account bucket (ADR-040) still applies at the gatewayd
    edge; this route charges 1 token via the apid authLimited
    middleware, same as every other `/v1/*` route.

    Args:
        before (UUID | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListInstancesResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            before=before,
            limit=limit,
        )
    ).parsed
