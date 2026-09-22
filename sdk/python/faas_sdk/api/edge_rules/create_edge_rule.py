from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_edge_rule_request import CreateEdgeRuleRequest
from ...models.edge_rule_response import EdgeRuleResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateEdgeRuleRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/edge-rules".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EdgeRuleResponse | Problem | None:
    if response.status_code == 201:
        response_201 = EdgeRuleResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EdgeRuleResponse | Problem]:
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
    body: CreateEdgeRuleRequest,
) -> Response[EdgeRuleResponse | Problem]:
    """Create an edge rule on an app.

     Kind is one of {route, rewrite, redirect, headers, cors, jwt, ip,
    validate, geo, async}. `action` is a kind-tagged jsonb body — the per-kind
    shape is documented under components/schemas. Plan-kind gate:
    jwt/ip return 402 plan_edge_rule_kind_not_allowed on Free; geo
    is allowed on Free with a tighter per-app quota. Per-app
    quota returns 402 plan_limit_edge_rules once EdgeRulesPerApp
    is reached.

    Args:
        slug (str):
        body (CreateEdgeRuleRequest): Body shape for POST /v1/apps/{slug}/edge-rules.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeRuleResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateEdgeRuleRequest,
) -> EdgeRuleResponse | Problem | None:
    """Create an edge rule on an app.

     Kind is one of {route, rewrite, redirect, headers, cors, jwt, ip,
    validate, geo, async}. `action` is a kind-tagged jsonb body — the per-kind
    shape is documented under components/schemas. Plan-kind gate:
    jwt/ip return 402 plan_edge_rule_kind_not_allowed on Free; geo
    is allowed on Free with a tighter per-app quota. Per-app
    quota returns 402 plan_limit_edge_rules once EdgeRulesPerApp
    is reached.

    Args:
        slug (str):
        body (CreateEdgeRuleRequest): Body shape for POST /v1/apps/{slug}/edge-rules.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeRuleResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateEdgeRuleRequest,
) -> Response[EdgeRuleResponse | Problem]:
    """Create an edge rule on an app.

     Kind is one of {route, rewrite, redirect, headers, cors, jwt, ip,
    validate, geo, async}. `action` is a kind-tagged jsonb body — the per-kind
    shape is documented under components/schemas. Plan-kind gate:
    jwt/ip return 402 plan_edge_rule_kind_not_allowed on Free; geo
    is allowed on Free with a tighter per-app quota. Per-app
    quota returns 402 plan_limit_edge_rules once EdgeRulesPerApp
    is reached.

    Args:
        slug (str):
        body (CreateEdgeRuleRequest): Body shape for POST /v1/apps/{slug}/edge-rules.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeRuleResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateEdgeRuleRequest,
) -> EdgeRuleResponse | Problem | None:
    """Create an edge rule on an app.

     Kind is one of {route, rewrite, redirect, headers, cors, jwt, ip,
    validate, geo, async}. `action` is a kind-tagged jsonb body — the per-kind
    shape is documented under components/schemas. Plan-kind gate:
    jwt/ip return 402 plan_edge_rule_kind_not_allowed on Free; geo
    is allowed on Free with a tighter per-app quota. Per-app
    quota returns 402 plan_limit_edge_rules once EdgeRulesPerApp
    is reached.

    Args:
        slug (str):
        body (CreateEdgeRuleRequest): Body shape for POST /v1/apps/{slug}/edge-rules.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeRuleResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
