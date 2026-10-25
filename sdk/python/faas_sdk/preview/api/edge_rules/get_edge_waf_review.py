from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.edge_waf_review_response import EdgeWAFReviewResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    rule: UUID | Unset = UNSET,
    since: str | Unset = "7d",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_rule: str | Unset = UNSET
    if not isinstance(rule, Unset):
        json_rule = str(rule)
    params["rule"] = json_rule

    params["since"] = since

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/edge-rules/waf-review".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EdgeWAFReviewResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EdgeWAFReviewResponse.from_dict(response.json())

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
) -> Response[EdgeWAFReviewResponse | Problem]:
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
    since: str | Unset = "7d",
) -> Response[EdgeWAFReviewResponse | Problem]:
    """The app's WAF detections grouped for a false-positive review.

     ADR-831 amendment 13. Reads the app's sampled kind=waf detections
    (detected, warned and blocked events, ADR-831 amendment 5) and groups
    them by edge rule, CRS rule and path, with id-like path segments read
    as {id}. Each group is assessed: likely_false_positive (a documented
    false positive, or at least 3 clients flagged on the path with none
    sending most of the detections), likely_attack (most detections from
    clients flagged on 5 or more paths), excluded (the rule already
    excludes the CRS rule) or review. The newest 5,000 detections of each
    outcome are read and at most 50 groups returned, largest first.
    Detections are sampled, so counts are lower bounds. The window
    defaults to 7 days and is clamped to the plan's event window.

    Args:
        slug (str):
        rule (UUID | Unset):
        since (str | Unset):  Default: '7d'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeWAFReviewResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        rule=rule,
        since=since,
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
    since: str | Unset = "7d",
) -> EdgeWAFReviewResponse | Problem | None:
    """The app's WAF detections grouped for a false-positive review.

     ADR-831 amendment 13. Reads the app's sampled kind=waf detections
    (detected, warned and blocked events, ADR-831 amendment 5) and groups
    them by edge rule, CRS rule and path, with id-like path segments read
    as {id}. Each group is assessed: likely_false_positive (a documented
    false positive, or at least 3 clients flagged on the path with none
    sending most of the detections), likely_attack (most detections from
    clients flagged on 5 or more paths), excluded (the rule already
    excludes the CRS rule) or review. The newest 5,000 detections of each
    outcome are read and at most 50 groups returned, largest first.
    Detections are sampled, so counts are lower bounds. The window
    defaults to 7 days and is clamped to the plan's event window.

    Args:
        slug (str):
        rule (UUID | Unset):
        since (str | Unset):  Default: '7d'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeWAFReviewResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        rule=rule,
        since=since,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    rule: UUID | Unset = UNSET,
    since: str | Unset = "7d",
) -> Response[EdgeWAFReviewResponse | Problem]:
    """The app's WAF detections grouped for a false-positive review.

     ADR-831 amendment 13. Reads the app's sampled kind=waf detections
    (detected, warned and blocked events, ADR-831 amendment 5) and groups
    them by edge rule, CRS rule and path, with id-like path segments read
    as {id}. Each group is assessed: likely_false_positive (a documented
    false positive, or at least 3 clients flagged on the path with none
    sending most of the detections), likely_attack (most detections from
    clients flagged on 5 or more paths), excluded (the rule already
    excludes the CRS rule) or review. The newest 5,000 detections of each
    outcome are read and at most 50 groups returned, largest first.
    Detections are sampled, so counts are lower bounds. The window
    defaults to 7 days and is clamped to the plan's event window.

    Args:
        slug (str):
        rule (UUID | Unset):
        since (str | Unset):  Default: '7d'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeWAFReviewResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        rule=rule,
        since=since,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    rule: UUID | Unset = UNSET,
    since: str | Unset = "7d",
) -> EdgeWAFReviewResponse | Problem | None:
    """The app's WAF detections grouped for a false-positive review.

     ADR-831 amendment 13. Reads the app's sampled kind=waf detections
    (detected, warned and blocked events, ADR-831 amendment 5) and groups
    them by edge rule, CRS rule and path, with id-like path segments read
    as {id}. Each group is assessed: likely_false_positive (a documented
    false positive, or at least 3 clients flagged on the path with none
    sending most of the detections), likely_attack (most detections from
    clients flagged on 5 or more paths), excluded (the rule already
    excludes the CRS rule) or review. The newest 5,000 detections of each
    outcome are read and at most 50 groups returned, largest first.
    Detections are sampled, so counts are lower bounds. The window
    defaults to 7 days and is clamped to the plan's event window.

    Args:
        slug (str):
        rule (UUID | Unset):
        since (str | Unset):  Default: '7d'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeWAFReviewResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            rule=rule,
            since=since,
        )
    ).parsed
