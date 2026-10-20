from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.edge_rule_events_response import EdgeRuleEventsResponse
from ...models.list_edge_rule_events_outcome import ListEdgeRuleEventsOutcome
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    rule: UUID | Unset = UNSET,
    outcome: ListEdgeRuleEventsOutcome | Unset = UNSET,
    since: str | Unset = "24h",
    limit: int | Unset = 50,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_rule: str | Unset = UNSET
    if not isinstance(rule, Unset):
        json_rule = str(rule)
    params["rule"] = json_rule

    json_outcome: str | Unset = UNSET
    if not isinstance(outcome, Unset):
        json_outcome = outcome

    params["outcome"] = json_outcome

    params["since"] = since

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/edge-rules/events".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EdgeRuleEventsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EdgeRuleEventsResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EdgeRuleEventsResponse | Problem]:
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
    rule: UUID | Unset = UNSET,
    outcome: ListEdgeRuleEventsOutcome | Unset = UNSET,
    since: str | Unset = "24h",
    limit: int | Unset = 50,
    cursor: str | Unset = UNSET,
) -> Response[EdgeRuleEventsResponse | Problem]:
    """Sampled requests each edge rule matched, newest first.

     ADR-964. Gateways keep the first 10 matches of each rule per minute
    as full events (request ID, method, host, path without query string,
    trusted client IP, country, user agent); hit counts (stats) cover
    every match. Events are kept 7 days; how far back a plan can read is
    a plan limit (Free 24 h, Hobby 72 h, Pro and Scale 7 days), and a
    longer since is clamped (the response reports the effective start).

    Args:
        slug (str):
        rule (UUID | Unset):
        outcome (ListEdgeRuleEventsOutcome | Unset):
        since (str | Unset):  Default: '24h'.
        limit (int | Unset):  Default: 50.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeRuleEventsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        rule=rule,
        outcome=outcome,
        since=since,
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
    rule: UUID | Unset = UNSET,
    outcome: ListEdgeRuleEventsOutcome | Unset = UNSET,
    since: str | Unset = "24h",
    limit: int | Unset = 50,
    cursor: str | Unset = UNSET,
) -> EdgeRuleEventsResponse | Problem | None:
    """Sampled requests each edge rule matched, newest first.

     ADR-964. Gateways keep the first 10 matches of each rule per minute
    as full events (request ID, method, host, path without query string,
    trusted client IP, country, user agent); hit counts (stats) cover
    every match. Events are kept 7 days; how far back a plan can read is
    a plan limit (Free 24 h, Hobby 72 h, Pro and Scale 7 days), and a
    longer since is clamped (the response reports the effective start).

    Args:
        slug (str):
        rule (UUID | Unset):
        outcome (ListEdgeRuleEventsOutcome | Unset):
        since (str | Unset):  Default: '24h'.
        limit (int | Unset):  Default: 50.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeRuleEventsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        rule=rule,
        outcome=outcome,
        since=since,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    rule: UUID | Unset = UNSET,
    outcome: ListEdgeRuleEventsOutcome | Unset = UNSET,
    since: str | Unset = "24h",
    limit: int | Unset = 50,
    cursor: str | Unset = UNSET,
) -> Response[EdgeRuleEventsResponse | Problem]:
    """Sampled requests each edge rule matched, newest first.

     ADR-964. Gateways keep the first 10 matches of each rule per minute
    as full events (request ID, method, host, path without query string,
    trusted client IP, country, user agent); hit counts (stats) cover
    every match. Events are kept 7 days; how far back a plan can read is
    a plan limit (Free 24 h, Hobby 72 h, Pro and Scale 7 days), and a
    longer since is clamped (the response reports the effective start).

    Args:
        slug (str):
        rule (UUID | Unset):
        outcome (ListEdgeRuleEventsOutcome | Unset):
        since (str | Unset):  Default: '24h'.
        limit (int | Unset):  Default: 50.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeRuleEventsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        rule=rule,
        outcome=outcome,
        since=since,
        limit=limit,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    rule: UUID | Unset = UNSET,
    outcome: ListEdgeRuleEventsOutcome | Unset = UNSET,
    since: str | Unset = "24h",
    limit: int | Unset = 50,
    cursor: str | Unset = UNSET,
) -> EdgeRuleEventsResponse | Problem | None:
    """Sampled requests each edge rule matched, newest first.

     ADR-964. Gateways keep the first 10 matches of each rule per minute
    as full events (request ID, method, host, path without query string,
    trusted client IP, country, user agent); hit counts (stats) cover
    every match. Events are kept 7 days; how far back a plan can read is
    a plan limit (Free 24 h, Hobby 72 h, Pro and Scale 7 days), and a
    longer since is clamped (the response reports the effective start).

    Args:
        slug (str):
        rule (UUID | Unset):
        outcome (ListEdgeRuleEventsOutcome | Unset):
        since (str | Unset):  Default: '24h'.
        limit (int | Unset):  Default: 50.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeRuleEventsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            rule=rule,
            outcome=outcome,
            since=since,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
