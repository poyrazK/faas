import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.request_audit_list_response import RequestAuditListResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 100,
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

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/audit/requests".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RequestAuditListResponse | None:
    if response.status_code == 200:
        response_200 = RequestAuditListResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RequestAuditListResponse]:
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
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 100,
) -> Response[Problem | RequestAuditListResponse]:
    """List exact gateway-observed request audit records (opt-in)

     Requires an MFA session or an authorized API key. Records are one per
    completed request, not collapsed debugger buckets. Only gateway-verified
    consumer and platform-tenant IDs are included. Application user,
    business action and internal/outbound dependency calls are not inferred.
    The trusted public-gateway source IP is included when available.
    The default window is 24 hours; at most 31 days may be
    queried at once. Undeclared route candidates can contain literal path
    segments; enable collection only after reviewing this privacy tradeoff.
    Exact records are removed after 30 days. The bounded list has no
    cursor export yet and is not a compliance/WORM archive.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RequestAuditListResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 100,
) -> Problem | RequestAuditListResponse | None:
    """List exact gateway-observed request audit records (opt-in)

     Requires an MFA session or an authorized API key. Records are one per
    completed request, not collapsed debugger buckets. Only gateway-verified
    consumer and platform-tenant IDs are included. Application user,
    business action and internal/outbound dependency calls are not inferred.
    The trusted public-gateway source IP is included when available.
    The default window is 24 hours; at most 31 days may be
    queried at once. Undeclared route candidates can contain literal path
    segments; enable collection only after reviewing this privacy tradeoff.
    Exact records are removed after 30 days. The bounded list has no
    cursor export yet and is not a compliance/WORM archive.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RequestAuditListResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        since=since,
        until=until,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 100,
) -> Response[Problem | RequestAuditListResponse]:
    """List exact gateway-observed request audit records (opt-in)

     Requires an MFA session or an authorized API key. Records are one per
    completed request, not collapsed debugger buckets. Only gateway-verified
    consumer and platform-tenant IDs are included. Application user,
    business action and internal/outbound dependency calls are not inferred.
    The trusted public-gateway source IP is included when available.
    The default window is 24 hours; at most 31 days may be
    queried at once. Undeclared route candidates can contain literal path
    segments; enable collection only after reviewing this privacy tradeoff.
    Exact records are removed after 30 days. The bounded list has no
    cursor export yet and is not a compliance/WORM archive.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RequestAuditListResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        since=since,
        until=until,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    limit: int | Unset = 100,
) -> Problem | RequestAuditListResponse | None:
    """List exact gateway-observed request audit records (opt-in)

     Requires an MFA session or an authorized API key. Records are one per
    completed request, not collapsed debugger buckets. Only gateway-verified
    consumer and platform-tenant IDs are included. Application user,
    business action and internal/outbound dependency calls are not inferred.
    The trusted public-gateway source IP is included when available.
    The default window is 24 hours; at most 31 days may be
    queried at once. Undeclared route candidates can contain literal path
    segments; enable collection only after reviewing this privacy tradeoff.
    Exact records are removed after 30 days. The bounded list has no
    cursor export yet and is not a compliance/WORM archive.

    Args:
        slug (str):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RequestAuditListResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            since=since,
            until=until,
            limit=limit,
        )
    ).parsed
