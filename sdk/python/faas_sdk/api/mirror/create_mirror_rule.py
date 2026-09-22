from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_mirror_rule_request import CreateMirrorRuleRequest
from ...models.mirror_rule_response import MirrorRuleResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateMirrorRuleRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/mirrors".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MirrorRuleResponse | Problem | None:
    if response.status_code == 201:
        response_201 = MirrorRuleResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
) -> Response[MirrorRuleResponse | Problem]:
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
    body: CreateMirrorRuleRequest,
) -> Response[MirrorRuleResponse | Problem]:
    r"""Create a mirror rule on an app.

     Binds a source deployment to a mirror deployment for canary-shadow
    comparison (issue #72 / ADR-125 / ADR-124 PR-A2). Both
    deployments must be `live` and belong to the same app. Plan gate
    fires 403 `plan_mirror_not_allowed` for Free/Hobby (cost: 1
    mirror VM per request, billed per running second, capped at
    `MirrorMaxLifetimeSeconds=5`). Per-app quota returns 422
    `mirror_rule_quota_exceeded` once `Limits.MirrorTargetsPerApp`
    is reached. The runtime dispatch (gateway goroutine, redaction,
    schedd stamping) lands in PR-A3 — A2 stores the rule + emits
    `mirror_rule.created` audit + pg_notify `kind=\"mirror\"` so PR-A3
    picks up the change within ~1s.

    Args:
        slug (str):
        body (CreateMirrorRuleRequest): Body for POST /v1/apps/{slug}/mirrors. Both deployments
            must
            be `live` and belong to the same app. `include_body` defaults
            to `false`; enabling it stores only request/response SHA-256
            hashes for body-difference classification, never raw bodies.
            `redact_headers` is the
            customer's additive list on top of the always-stripped list
            (Authorization, Cookie, Set-Cookie, X-API-Key, Proxy-Authorization,
            WWW-Authenticate — applied by PR-A3's redaction layer, NOT by
            A2's storage layer).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MirrorRuleResponse | Problem]
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
    body: CreateMirrorRuleRequest,
) -> MirrorRuleResponse | Problem | None:
    r"""Create a mirror rule on an app.

     Binds a source deployment to a mirror deployment for canary-shadow
    comparison (issue #72 / ADR-125 / ADR-124 PR-A2). Both
    deployments must be `live` and belong to the same app. Plan gate
    fires 403 `plan_mirror_not_allowed` for Free/Hobby (cost: 1
    mirror VM per request, billed per running second, capped at
    `MirrorMaxLifetimeSeconds=5`). Per-app quota returns 422
    `mirror_rule_quota_exceeded` once `Limits.MirrorTargetsPerApp`
    is reached. The runtime dispatch (gateway goroutine, redaction,
    schedd stamping) lands in PR-A3 — A2 stores the rule + emits
    `mirror_rule.created` audit + pg_notify `kind=\"mirror\"` so PR-A3
    picks up the change within ~1s.

    Args:
        slug (str):
        body (CreateMirrorRuleRequest): Body for POST /v1/apps/{slug}/mirrors. Both deployments
            must
            be `live` and belong to the same app. `include_body` defaults
            to `false`; enabling it stores only request/response SHA-256
            hashes for body-difference classification, never raw bodies.
            `redact_headers` is the
            customer's additive list on top of the always-stripped list
            (Authorization, Cookie, Set-Cookie, X-API-Key, Proxy-Authorization,
            WWW-Authenticate — applied by PR-A3's redaction layer, NOT by
            A2's storage layer).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MirrorRuleResponse | Problem
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
    body: CreateMirrorRuleRequest,
) -> Response[MirrorRuleResponse | Problem]:
    r"""Create a mirror rule on an app.

     Binds a source deployment to a mirror deployment for canary-shadow
    comparison (issue #72 / ADR-125 / ADR-124 PR-A2). Both
    deployments must be `live` and belong to the same app. Plan gate
    fires 403 `plan_mirror_not_allowed` for Free/Hobby (cost: 1
    mirror VM per request, billed per running second, capped at
    `MirrorMaxLifetimeSeconds=5`). Per-app quota returns 422
    `mirror_rule_quota_exceeded` once `Limits.MirrorTargetsPerApp`
    is reached. The runtime dispatch (gateway goroutine, redaction,
    schedd stamping) lands in PR-A3 — A2 stores the rule + emits
    `mirror_rule.created` audit + pg_notify `kind=\"mirror\"` so PR-A3
    picks up the change within ~1s.

    Args:
        slug (str):
        body (CreateMirrorRuleRequest): Body for POST /v1/apps/{slug}/mirrors. Both deployments
            must
            be `live` and belong to the same app. `include_body` defaults
            to `false`; enabling it stores only request/response SHA-256
            hashes for body-difference classification, never raw bodies.
            `redact_headers` is the
            customer's additive list on top of the always-stripped list
            (Authorization, Cookie, Set-Cookie, X-API-Key, Proxy-Authorization,
            WWW-Authenticate — applied by PR-A3's redaction layer, NOT by
            A2's storage layer).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MirrorRuleResponse | Problem]
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
    body: CreateMirrorRuleRequest,
) -> MirrorRuleResponse | Problem | None:
    r"""Create a mirror rule on an app.

     Binds a source deployment to a mirror deployment for canary-shadow
    comparison (issue #72 / ADR-125 / ADR-124 PR-A2). Both
    deployments must be `live` and belong to the same app. Plan gate
    fires 403 `plan_mirror_not_allowed` for Free/Hobby (cost: 1
    mirror VM per request, billed per running second, capped at
    `MirrorMaxLifetimeSeconds=5`). Per-app quota returns 422
    `mirror_rule_quota_exceeded` once `Limits.MirrorTargetsPerApp`
    is reached. The runtime dispatch (gateway goroutine, redaction,
    schedd stamping) lands in PR-A3 — A2 stores the rule + emits
    `mirror_rule.created` audit + pg_notify `kind=\"mirror\"` so PR-A3
    picks up the change within ~1s.

    Args:
        slug (str):
        body (CreateMirrorRuleRequest): Body for POST /v1/apps/{slug}/mirrors. Both deployments
            must
            be `live` and belong to the same app. `include_body` defaults
            to `false`; enabling it stores only request/response SHA-256
            hashes for body-difference classification, never raw bodies.
            `redact_headers` is the
            customer's additive list on top of the always-stripped list
            (Authorization, Cookie, Set-Cookie, X-API-Key, Proxy-Authorization,
            WWW-Authenticate — applied by PR-A3's redaction layer, NOT by
            A2's storage layer).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MirrorRuleResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
