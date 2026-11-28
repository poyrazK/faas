from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.issue_split_rule import IssueSplitRule
from ...models.issue_split_rule_update import IssueSplitRuleUpdate
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    issue_id: UUID,
    rule_id: UUID,
    *,
    body: IssueSplitRuleUpdate,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/apps/{slug}/issues/{issue_id}/split-rules/{rule_id}".format(
            slug=quote(str(slug), safe=""),
            issue_id=quote(str(issue_id), safe=""),
            rule_id=quote(str(rule_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> IssueSplitRule | Problem | None:
    if response.status_code == 200:
        response_200 = IssueSplitRule.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[IssueSplitRule | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    issue_id: UUID,
    rule_id: UUID,
    *,
    client: AuthenticatedClient,
    body: IssueSplitRuleUpdate,
) -> Response[IssueSplitRule | Problem]:
    """Enable or disable an existing occurrence routing rule.

     Changes future routing only. Existing evidence, prior retry destinations, matcher, and priority
    remain unchanged.

    Args:
        slug (str):
        issue_id (UUID):
        rule_id (UUID):
        body (IssueSplitRuleUpdate): Enable or disable routing for future captures without moving
            existing occurrences or changing the immutable rule definition.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IssueSplitRule | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        issue_id=issue_id,
        rule_id=rule_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    issue_id: UUID,
    rule_id: UUID,
    *,
    client: AuthenticatedClient,
    body: IssueSplitRuleUpdate,
) -> IssueSplitRule | Problem | None:
    """Enable or disable an existing occurrence routing rule.

     Changes future routing only. Existing evidence, prior retry destinations, matcher, and priority
    remain unchanged.

    Args:
        slug (str):
        issue_id (UUID):
        rule_id (UUID):
        body (IssueSplitRuleUpdate): Enable or disable routing for future captures without moving
            existing occurrences or changing the immutable rule definition.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IssueSplitRule | Problem
    """

    return sync_detailed(
        slug=slug,
        issue_id=issue_id,
        rule_id=rule_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    issue_id: UUID,
    rule_id: UUID,
    *,
    client: AuthenticatedClient,
    body: IssueSplitRuleUpdate,
) -> Response[IssueSplitRule | Problem]:
    """Enable or disable an existing occurrence routing rule.

     Changes future routing only. Existing evidence, prior retry destinations, matcher, and priority
    remain unchanged.

    Args:
        slug (str):
        issue_id (UUID):
        rule_id (UUID):
        body (IssueSplitRuleUpdate): Enable or disable routing for future captures without moving
            existing occurrences or changing the immutable rule definition.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IssueSplitRule | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        issue_id=issue_id,
        rule_id=rule_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    issue_id: UUID,
    rule_id: UUID,
    *,
    client: AuthenticatedClient,
    body: IssueSplitRuleUpdate,
) -> IssueSplitRule | Problem | None:
    """Enable or disable an existing occurrence routing rule.

     Changes future routing only. Existing evidence, prior retry destinations, matcher, and priority
    remain unchanged.

    Args:
        slug (str):
        issue_id (UUID):
        rule_id (UUID):
        body (IssueSplitRuleUpdate): Enable or disable routing for future captures without moving
            existing occurrences or changing the immutable rule definition.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IssueSplitRule | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            issue_id=issue_id,
            rule_id=rule_id,
            client=client,
            body=body,
        )
    ).parsed
