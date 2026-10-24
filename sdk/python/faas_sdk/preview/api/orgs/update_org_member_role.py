from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.change_member_role_request import ChangeMemberRoleRequest
from ...models.org_member_response import OrgMemberResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    user_id: str,
    *,
    body: ChangeMemberRoleRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/orgs/{slug}/members/{user_id}".format(
            slug=quote(str(slug), safe=""),
            user_id=quote(str(user_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OrgMemberResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OrgMemberResponse.from_dict(response.json())

        return response_200

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OrgMemberResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    user_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ChangeMemberRoleRequest,
) -> Response[OrgMemberResponse | Problem]:
    """Change a member's role.

     Owner-only (`org.change_role`). Role cannot be `owner` on
    this endpoint; transfer-ownership is the only path to owner.
    The exactly-one-owner invariant lives in
    `pkg/state::UpdateOrgMemberRole`'s tx; demoting the last
    active owner surfaces as 409 `org_last_owner`.

    Args:
        slug (str):
        user_id (str):
        body (ChangeMemberRoleRequest): PATCH /v1/orgs/{slug}/members/{user_id} body. Role cannot
            be `owner` on this endpoint — transfer-ownership is the
            only path to owner.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgMemberResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        user_id=user_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    user_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ChangeMemberRoleRequest,
) -> OrgMemberResponse | Problem | None:
    """Change a member's role.

     Owner-only (`org.change_role`). Role cannot be `owner` on
    this endpoint; transfer-ownership is the only path to owner.
    The exactly-one-owner invariant lives in
    `pkg/state::UpdateOrgMemberRole`'s tx; demoting the last
    active owner surfaces as 409 `org_last_owner`.

    Args:
        slug (str):
        user_id (str):
        body (ChangeMemberRoleRequest): PATCH /v1/orgs/{slug}/members/{user_id} body. Role cannot
            be `owner` on this endpoint — transfer-ownership is the
            only path to owner.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgMemberResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        user_id=user_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    user_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ChangeMemberRoleRequest,
) -> Response[OrgMemberResponse | Problem]:
    """Change a member's role.

     Owner-only (`org.change_role`). Role cannot be `owner` on
    this endpoint; transfer-ownership is the only path to owner.
    The exactly-one-owner invariant lives in
    `pkg/state::UpdateOrgMemberRole`'s tx; demoting the last
    active owner surfaces as 409 `org_last_owner`.

    Args:
        slug (str):
        user_id (str):
        body (ChangeMemberRoleRequest): PATCH /v1/orgs/{slug}/members/{user_id} body. Role cannot
            be `owner` on this endpoint — transfer-ownership is the
            only path to owner.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgMemberResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        user_id=user_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    user_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ChangeMemberRoleRequest,
) -> OrgMemberResponse | Problem | None:
    """Change a member's role.

     Owner-only (`org.change_role`). Role cannot be `owner` on
    this endpoint; transfer-ownership is the only path to owner.
    The exactly-one-owner invariant lives in
    `pkg/state::UpdateOrgMemberRole`'s tx; demoting the last
    active owner surfaces as 409 `org_last_owner`.

    Args:
        slug (str):
        user_id (str):
        body (ChangeMemberRoleRequest): PATCH /v1/orgs/{slug}/members/{user_id} body. Role cannot
            be `owner` on this endpoint — transfer-ownership is the
            only path to owner.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgMemberResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            user_id=user_id,
            client=client,
            body=body,
        )
    ).parsed
