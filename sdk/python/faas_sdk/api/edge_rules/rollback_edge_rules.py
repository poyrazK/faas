from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.edge_rule_response import EdgeRuleResponse
from ...models.problem import Problem
from ...models.rollback_edge_rules_request import RollbackEdgeRulesRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: RollbackEdgeRulesRequest,
    if_match: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(if_match, Unset):
        headers["If-Match"] = if_match

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/edge-rules/rollback".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | list[EdgeRuleResponse] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = EdgeRuleResponse.from_dict(response_200_item_data)

            response_200.append(response_200_item)

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 412:
        response_412 = Problem.from_dict(response.json())

        return response_412

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | list[EdgeRuleResponse]]:
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
    body: RollbackEdgeRulesRequest,
    if_match: str | Unset = UNSET,
) -> Response[Problem | list[EdgeRuleResponse]]:
    """Restore an app's edge rules to a recorded version.

     Replaces every current rule with the version's rules in one
    transaction, preserving rule IDs, behind fleet convergence. The
    restore is recorded as a new version. Refused when the version
    exceeds the current plan's edge-rule quotas or references a deleted
    CORS preset.

    Args:
        slug (str):
        if_match (str | Unset):
        body (RollbackEdgeRulesRequest): Restore an app's edge rules to a recorded rule-set
            version (ADR-961).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[EdgeRuleResponse]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        if_match=if_match,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RollbackEdgeRulesRequest,
    if_match: str | Unset = UNSET,
) -> Problem | list[EdgeRuleResponse] | None:
    """Restore an app's edge rules to a recorded version.

     Replaces every current rule with the version's rules in one
    transaction, preserving rule IDs, behind fleet convergence. The
    restore is recorded as a new version. Refused when the version
    exceeds the current plan's edge-rule quotas or references a deleted
    CORS preset.

    Args:
        slug (str):
        if_match (str | Unset):
        body (RollbackEdgeRulesRequest): Restore an app's edge rules to a recorded rule-set
            version (ADR-961).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[EdgeRuleResponse]
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        if_match=if_match,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RollbackEdgeRulesRequest,
    if_match: str | Unset = UNSET,
) -> Response[Problem | list[EdgeRuleResponse]]:
    """Restore an app's edge rules to a recorded version.

     Replaces every current rule with the version's rules in one
    transaction, preserving rule IDs, behind fleet convergence. The
    restore is recorded as a new version. Refused when the version
    exceeds the current plan's edge-rule quotas or references a deleted
    CORS preset.

    Args:
        slug (str):
        if_match (str | Unset):
        body (RollbackEdgeRulesRequest): Restore an app's edge rules to a recorded rule-set
            version (ADR-961).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[EdgeRuleResponse]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        if_match=if_match,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RollbackEdgeRulesRequest,
    if_match: str | Unset = UNSET,
) -> Problem | list[EdgeRuleResponse] | None:
    """Restore an app's edge rules to a recorded version.

     Replaces every current rule with the version's rules in one
    transaction, preserving rule IDs, behind fleet convergence. The
    restore is recorded as a new version. Refused when the version
    exceeds the current plan's edge-rule quotas or references a deleted
    CORS preset.

    Args:
        slug (str):
        if_match (str | Unset):
        body (RollbackEdgeRulesRequest): Restore an app's edge rules to a recorded rule-set
            version (ADR-961).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[EdgeRuleResponse]
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            if_match=if_match,
        )
    ).parsed
