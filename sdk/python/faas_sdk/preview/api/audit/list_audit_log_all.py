import datetime
from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_audit_log_response import ListAuditLogResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    account_id: UUID | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    before: str | Unset = UNSET,
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
    include_anonymous: bool | Unset = False,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_account_id: str | Unset = UNSET
    if not isinstance(account_id, Unset):
        json_account_id = str(account_id)
    params["account_id"] = json_account_id

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    params["before"] = before

    params["kind_prefix"] = kind_prefix

    params["limit"] = limit

    params["include_anonymous"] = include_anonymous

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/audit-log/all",
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

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
    account_id: UUID | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    before: str | Unset = UNSET,
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
    include_anonymous: bool | Unset = False,
) -> Response[ListAuditLogResponse | Problem]:
    """Operator view of every audit-log entry (cross-account).

     Admin-only read of the FK-free `audit_log` table. Reads
    across accounts by default; pass `?account_id=<uuid>` to
    restrict to one account. `?include_anonymous=true` surfaces
    the `account_id IS NULL` rows emitted by background /
    anonymous activity (the customer-scoped `/v1/audit-log`
    endpoint never returns those rows).

    Scope: session cookie (implicitly admin) or an admin API
    key (`api.ScopesAdminOnly` — `{admin}`). Not MFA-gated at
    the handler level because admin sessions / admin keys are
    already MFA-gated upstream at session / key issue time.

    Args:
        account_id (UUID | Unset):
        since (datetime.datetime | Unset):
        before (str | Unset):
        kind_prefix (str | Unset):
        limit (int | Unset):  Default: 50.
        include_anonymous (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListAuditLogResponse | Problem]
    """

    kwargs = _get_kwargs(
        account_id=account_id,
        since=since,
        before=before,
        kind_prefix=kind_prefix,
        limit=limit,
        include_anonymous=include_anonymous,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    account_id: UUID | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    before: str | Unset = UNSET,
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
    include_anonymous: bool | Unset = False,
) -> ListAuditLogResponse | Problem | None:
    """Operator view of every audit-log entry (cross-account).

     Admin-only read of the FK-free `audit_log` table. Reads
    across accounts by default; pass `?account_id=<uuid>` to
    restrict to one account. `?include_anonymous=true` surfaces
    the `account_id IS NULL` rows emitted by background /
    anonymous activity (the customer-scoped `/v1/audit-log`
    endpoint never returns those rows).

    Scope: session cookie (implicitly admin) or an admin API
    key (`api.ScopesAdminOnly` — `{admin}`). Not MFA-gated at
    the handler level because admin sessions / admin keys are
    already MFA-gated upstream at session / key issue time.

    Args:
        account_id (UUID | Unset):
        since (datetime.datetime | Unset):
        before (str | Unset):
        kind_prefix (str | Unset):
        limit (int | Unset):  Default: 50.
        include_anonymous (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListAuditLogResponse | Problem
    """

    return sync_detailed(
        client=client,
        account_id=account_id,
        since=since,
        before=before,
        kind_prefix=kind_prefix,
        limit=limit,
        include_anonymous=include_anonymous,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    account_id: UUID | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    before: str | Unset = UNSET,
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
    include_anonymous: bool | Unset = False,
) -> Response[ListAuditLogResponse | Problem]:
    """Operator view of every audit-log entry (cross-account).

     Admin-only read of the FK-free `audit_log` table. Reads
    across accounts by default; pass `?account_id=<uuid>` to
    restrict to one account. `?include_anonymous=true` surfaces
    the `account_id IS NULL` rows emitted by background /
    anonymous activity (the customer-scoped `/v1/audit-log`
    endpoint never returns those rows).

    Scope: session cookie (implicitly admin) or an admin API
    key (`api.ScopesAdminOnly` — `{admin}`). Not MFA-gated at
    the handler level because admin sessions / admin keys are
    already MFA-gated upstream at session / key issue time.

    Args:
        account_id (UUID | Unset):
        since (datetime.datetime | Unset):
        before (str | Unset):
        kind_prefix (str | Unset):
        limit (int | Unset):  Default: 50.
        include_anonymous (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListAuditLogResponse | Problem]
    """

    kwargs = _get_kwargs(
        account_id=account_id,
        since=since,
        before=before,
        kind_prefix=kind_prefix,
        limit=limit,
        include_anonymous=include_anonymous,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    account_id: UUID | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    before: str | Unset = UNSET,
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
    include_anonymous: bool | Unset = False,
) -> ListAuditLogResponse | Problem | None:
    """Operator view of every audit-log entry (cross-account).

     Admin-only read of the FK-free `audit_log` table. Reads
    across accounts by default; pass `?account_id=<uuid>` to
    restrict to one account. `?include_anonymous=true` surfaces
    the `account_id IS NULL` rows emitted by background /
    anonymous activity (the customer-scoped `/v1/audit-log`
    endpoint never returns those rows).

    Scope: session cookie (implicitly admin) or an admin API
    key (`api.ScopesAdminOnly` — `{admin}`). Not MFA-gated at
    the handler level because admin sessions / admin keys are
    already MFA-gated upstream at session / key issue time.

    Args:
        account_id (UUID | Unset):
        since (datetime.datetime | Unset):
        before (str | Unset):
        kind_prefix (str | Unset):
        limit (int | Unset):  Default: 50.
        include_anonymous (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListAuditLogResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            account_id=account_id,
            since=since,
            before=before,
            kind_prefix=kind_prefix,
            limit=limit,
            include_anonymous=include_anonymous,
        )
    ).parsed
