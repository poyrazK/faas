import datetime
from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_audit_events_response import ListAuditEventsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    since: datetime.datetime | Unset = UNSET,
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    params["kind_prefix"] = kind_prefix

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/audit-events",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListAuditEventsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListAuditEventsResponse.from_dict(response.json())

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
) -> Response[ListAuditEventsResponse | Problem]:
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
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[ListAuditEventsResponse | Problem]:
    """List the caller's auth audit events.

     Newest-first. Returns the customer's own security event timeline:
    logins, logouts, key mints/deletes, secret sets/deletes, plan
    changes, and account deletion scheduling/restore.

    The events table is append-only (spec §5 / §6.1). The response
    is bounded by `limit` (default 50, max 100). For a full-history
    pull, use `GET /v1/account/export`, which unions these events
    with the customer's GDPR-action rows.

    Args:
        since (datetime.datetime | Unset):
        kind_prefix (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListAuditEventsResponse | Problem]
    """

    kwargs = _get_kwargs(
        since=since,
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
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> ListAuditEventsResponse | Problem | None:
    """List the caller's auth audit events.

     Newest-first. Returns the customer's own security event timeline:
    logins, logouts, key mints/deletes, secret sets/deletes, plan
    changes, and account deletion scheduling/restore.

    The events table is append-only (spec §5 / §6.1). The response
    is bounded by `limit` (default 50, max 100). For a full-history
    pull, use `GET /v1/account/export`, which unions these events
    with the customer's GDPR-action rows.

    Args:
        since (datetime.datetime | Unset):
        kind_prefix (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListAuditEventsResponse | Problem
    """

    return sync_detailed(
        client=client,
        since=since,
        kind_prefix=kind_prefix,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[ListAuditEventsResponse | Problem]:
    """List the caller's auth audit events.

     Newest-first. Returns the customer's own security event timeline:
    logins, logouts, key mints/deletes, secret sets/deletes, plan
    changes, and account deletion scheduling/restore.

    The events table is append-only (spec §5 / §6.1). The response
    is bounded by `limit` (default 50, max 100). For a full-history
    pull, use `GET /v1/account/export`, which unions these events
    with the customer's GDPR-action rows.

    Args:
        since (datetime.datetime | Unset):
        kind_prefix (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListAuditEventsResponse | Problem]
    """

    kwargs = _get_kwargs(
        since=since,
        kind_prefix=kind_prefix,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    kind_prefix: str | Unset = UNSET,
    limit: int | Unset = 50,
) -> ListAuditEventsResponse | Problem | None:
    """List the caller's auth audit events.

     Newest-first. Returns the customer's own security event timeline:
    logins, logouts, key mints/deletes, secret sets/deletes, plan
    changes, and account deletion scheduling/restore.

    The events table is append-only (spec §5 / §6.1). The response
    is bounded by `limit` (default 50, max 100). For a full-history
    pull, use `GET /v1/account/export`, which unions these events
    with the customer's GDPR-action rows.

    Args:
        since (datetime.datetime | Unset):
        kind_prefix (str | Unset):
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListAuditEventsResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            since=since,
            kind_prefix=kind_prefix,
            limit=limit,
        )
    ).parsed
