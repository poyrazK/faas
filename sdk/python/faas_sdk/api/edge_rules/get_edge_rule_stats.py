from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.edge_rule_stats_response import EdgeRuleStatsResponse
from ...models.get_edge_rule_stats_window import GetEdgeRuleStatsWindow
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    window: GetEdgeRuleStatsWindow | Unset = "24h",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_window: str | Unset = UNSET
    if not isinstance(window, Unset):
        json_window = window

    params["window"] = json_window

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/edge-rules/stats".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EdgeRuleStatsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EdgeRuleStatsResponse.from_dict(response.json())

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
) -> Response[EdgeRuleStatsResponse | Problem]:
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
    window: GetEdgeRuleStatsWindow | Unset = "24h",
) -> Response[EdgeRuleStatsResponse | Problem]:
    """Per-rule match counts for an app over a window.

     ADR-904. Gateways count each rule's matches (matched for enforced
    rules, logged for log-mode rules), at most once per rule per request,
    and flush them into hourly buckets once a minute; buckets are kept
    for 14 days. Rules with no matches in the window are omitted.

    Args:
        slug (str):
        window (GetEdgeRuleStatsWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeRuleStatsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        window=window,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    window: GetEdgeRuleStatsWindow | Unset = "24h",
) -> EdgeRuleStatsResponse | Problem | None:
    """Per-rule match counts for an app over a window.

     ADR-904. Gateways count each rule's matches (matched for enforced
    rules, logged for log-mode rules), at most once per rule per request,
    and flush them into hourly buckets once a minute; buckets are kept
    for 14 days. Rules with no matches in the window are omitted.

    Args:
        slug (str):
        window (GetEdgeRuleStatsWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeRuleStatsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        window=window,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    window: GetEdgeRuleStatsWindow | Unset = "24h",
) -> Response[EdgeRuleStatsResponse | Problem]:
    """Per-rule match counts for an app over a window.

     ADR-904. Gateways count each rule's matches (matched for enforced
    rules, logged for log-mode rules), at most once per rule per request,
    and flush them into hourly buckets once a minute; buckets are kept
    for 14 days. Rules with no matches in the window are omitted.

    Args:
        slug (str):
        window (GetEdgeRuleStatsWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeRuleStatsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        window=window,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    window: GetEdgeRuleStatsWindow | Unset = "24h",
) -> EdgeRuleStatsResponse | Problem | None:
    """Per-rule match counts for an app over a window.

     ADR-904. Gateways count each rule's matches (matched for enforced
    rules, logged for log-mode rules), at most once per rule per request,
    and flush them into hourly buckets once a minute; buckets are kept
    for 14 days. Rules with no matches in the window are omitted.

    Args:
        slug (str):
        window (GetEdgeRuleStatsWindow | Unset):  Default: '24h'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeRuleStatsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            window=window,
        )
    ).parsed
