import datetime
from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_audit_log_response import ListAuditLogResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    since: datetime.datetime | Unset = UNSET,
    before: str | Unset = UNSET,
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    params["before"] = before

    params["kind_prefix"] = kind_prefix

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/audit-log",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListAuditLogResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListAuditLogResponse.from_dict(response.json())

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
) -> Response[ListAuditLogResponse | Problem]:
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
    before: str | Unset = UNSET,
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[ListAuditLogResponse | Problem]:
    """List the caller's audit-log entries (post-deletion evidence).

     Newest-first. Reads the FK-free `audit_log` table
    (migrations/00163_audit_log.sql), distinct from
    `/v1/audit-events` which reads the live `events` table. The
    `audit_log` table is append-only by spec (ISO 27001 SoA
    A.5.33 — retention forever) and a regulator / DPO can replay
    post-deletion state from the row alone.

    Scope: session cookie (implicitly admin) or any API key
    carrying `{admin, apps:read}` (`api.ScopesReadSurface`).
    MFA-gated. Cross-account invisibility is enforced by pinning
    `account_id` to the calling account's id inside the handler;
    the SQL filter rejects `account_id IS NULL` rows by default
    (a customer never sees anonymous rows).

    Args:
        since (datetime.datetime | Unset):
        before (str | Unset):
        kind_prefix (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListAuditLogResponse | Problem]
    """

    kwargs = _get_kwargs(
        since=since,
        before=before,
        kind_prefix=kind_prefix,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    before: str | Unset = UNSET,
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> ListAuditLogResponse | Problem | None:
    """List the caller's audit-log entries (post-deletion evidence).

     Newest-first. Reads the FK-free `audit_log` table
    (migrations/00163_audit_log.sql), distinct from
    `/v1/audit-events` which reads the live `events` table. The
    `audit_log` table is append-only by spec (ISO 27001 SoA
    A.5.33 — retention forever) and a regulator / DPO can replay
    post-deletion state from the row alone.

    Scope: session cookie (implicitly admin) or any API key
    carrying `{admin, apps:read}` (`api.ScopesReadSurface`).
    MFA-gated. Cross-account invisibility is enforced by pinning
    `account_id` to the calling account's id inside the handler;
    the SQL filter rejects `account_id IS NULL` rows by default
    (a customer never sees anonymous rows).

    Args:
        since (datetime.datetime | Unset):
        before (str | Unset):
        kind_prefix (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListAuditLogResponse | Problem
    """

    return sync_detailed(
        client=client,
        since=since,
        before=before,
        kind_prefix=kind_prefix,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    before: str | Unset = UNSET,
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[ListAuditLogResponse | Problem]:
    """List the caller's audit-log entries (post-deletion evidence).

     Newest-first. Reads the FK-free `audit_log` table
    (migrations/00163_audit_log.sql), distinct from
    `/v1/audit-events` which reads the live `events` table. The
    `audit_log` table is append-only by spec (ISO 27001 SoA
    A.5.33 — retention forever) and a regulator / DPO can replay
    post-deletion state from the row alone.

    Scope: session cookie (implicitly admin) or any API key
    carrying `{admin, apps:read}` (`api.ScopesReadSurface`).
    MFA-gated. Cross-account invisibility is enforced by pinning
    `account_id` to the calling account's id inside the handler;
    the SQL filter rejects `account_id IS NULL` rows by default
    (a customer never sees anonymous rows).

    Args:
        since (datetime.datetime | Unset):
        before (str | Unset):
        kind_prefix (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListAuditLogResponse | Problem]
    """

    kwargs = _get_kwargs(
        since=since,
        before=before,
        kind_prefix=kind_prefix,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    before: str | Unset = UNSET,
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> ListAuditLogResponse | Problem | None:
    """List the caller's audit-log entries (post-deletion evidence).

     Newest-first. Reads the FK-free `audit_log` table
    (migrations/00163_audit_log.sql), distinct from
    `/v1/audit-events` which reads the live `events` table. The
    `audit_log` table is append-only by spec (ISO 27001 SoA
    A.5.33 — retention forever) and a regulator / DPO can replay
    post-deletion state from the row alone.

    Scope: session cookie (implicitly admin) or any API key
    carrying `{admin, apps:read}` (`api.ScopesReadSurface`).
    MFA-gated. Cross-account invisibility is enforced by pinning
    `account_id` to the calling account's id inside the handler;
    the SQL filter rejects `account_id IS NULL` rows by default
    (a customer never sees anonymous rows).

    Args:
        since (datetime.datetime | Unset):
        before (str | Unset):
        kind_prefix (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListAuditLogResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            since=since,
            before=before,
            kind_prefix=kind_prefix,
            limit=limit,
        )
    ).parsed
