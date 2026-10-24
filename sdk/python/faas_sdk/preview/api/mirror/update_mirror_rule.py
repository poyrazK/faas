from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mirror_rule_response import MirrorRuleResponse
from ...models.problem import Problem
from ...models.update_mirror_rule_request import UpdateMirrorRuleRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    *,
    body: UpdateMirrorRuleRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/apps/{slug}/mirrors/{id}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MirrorRuleResponse | Problem | None:
    if response.status_code == 200:
        response_200 = MirrorRuleResponse.from_dict(response.json())

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
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateMirrorRuleRequest,
) -> Response[MirrorRuleResponse | Problem]:
    r"""Patch a mirror rule.

     Patch semantics — pointer fields distinguish \"absent\" from
    \"set to zero\". `percent=0` is a legal value (disable-without-
    removing); a missing `percent` keeps the existing value. The
    plan gate is intentionally NOT enforced on update: a Pro
    customer's existing rule survives an upgrade to Hobby; the
    reaper disables the rule on the next read window so the mirror
    VM doesn't keep waking.

    Args:
        slug (str):
        id (str):
        body (UpdateMirrorRuleRequest): Body for PATCH /v1/apps/{slug}/mirrors/{id}. All fields
            are
            optional; pointer-style patches mean an absent key keeps the
            existing value, while an explicit zero/empty overrides. Setting
            `redact_headers` to `[]` clears the customer's list (leaving
            only the always-stripped list).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MirrorRuleResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateMirrorRuleRequest,
) -> MirrorRuleResponse | Problem | None:
    r"""Patch a mirror rule.

     Patch semantics — pointer fields distinguish \"absent\" from
    \"set to zero\". `percent=0` is a legal value (disable-without-
    removing); a missing `percent` keeps the existing value. The
    plan gate is intentionally NOT enforced on update: a Pro
    customer's existing rule survives an upgrade to Hobby; the
    reaper disables the rule on the next read window so the mirror
    VM doesn't keep waking.

    Args:
        slug (str):
        id (str):
        body (UpdateMirrorRuleRequest): Body for PATCH /v1/apps/{slug}/mirrors/{id}. All fields
            are
            optional; pointer-style patches mean an absent key keeps the
            existing value, while an explicit zero/empty overrides. Setting
            `redact_headers` to `[]` clears the customer's list (leaving
            only the always-stripped list).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MirrorRuleResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateMirrorRuleRequest,
) -> Response[MirrorRuleResponse | Problem]:
    r"""Patch a mirror rule.

     Patch semantics — pointer fields distinguish \"absent\" from
    \"set to zero\". `percent=0` is a legal value (disable-without-
    removing); a missing `percent` keeps the existing value. The
    plan gate is intentionally NOT enforced on update: a Pro
    customer's existing rule survives an upgrade to Hobby; the
    reaper disables the rule on the next read window so the mirror
    VM doesn't keep waking.

    Args:
        slug (str):
        id (str):
        body (UpdateMirrorRuleRequest): Body for PATCH /v1/apps/{slug}/mirrors/{id}. All fields
            are
            optional; pointer-style patches mean an absent key keeps the
            existing value, while an explicit zero/empty overrides. Setting
            `redact_headers` to `[]` clears the customer's list (leaving
            only the always-stripped list).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MirrorRuleResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateMirrorRuleRequest,
) -> MirrorRuleResponse | Problem | None:
    r"""Patch a mirror rule.

     Patch semantics — pointer fields distinguish \"absent\" from
    \"set to zero\". `percent=0` is a legal value (disable-without-
    removing); a missing `percent` keeps the existing value. The
    plan gate is intentionally NOT enforced on update: a Pro
    customer's existing rule survives an upgrade to Hobby; the
    reaper disables the rule on the next read window so the mirror
    VM doesn't keep waking.

    Args:
        slug (str):
        id (str):
        body (UpdateMirrorRuleRequest): Body for PATCH /v1/apps/{slug}/mirrors/{id}. All fields
            are
            optional; pointer-style patches mean an absent key keeps the
            existing value, while an explicit zero/empty overrides. Setting
            `redact_headers` to `[]` clears the customer's list (leaving
            only the always-stripped list).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MirrorRuleResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
