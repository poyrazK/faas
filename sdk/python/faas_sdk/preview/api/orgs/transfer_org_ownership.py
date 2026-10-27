from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.org_response import OrgResponse
from ...models.problem import Problem
from ...models.transfer_ownership_request import TransferOwnershipRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: TransferOwnershipRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{slug}/transfer_ownership".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> OrgResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OrgResponse.from_dict(response.json())

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
) -> Response[OrgResponse | Problem]:
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
    body: TransferOwnershipRequest,
) -> Response[OrgResponse | Problem]:
    """Transfer ownership to another active member.

     Atomically promotes `new_owner_account_id` to owner and
    demotes the caller to admin via `Store.TransferOrgOwnership`
    (a single PostgreSQL tx with `FOR UPDATE` locks on both
    rows). The exactly-one-owner invariant is enforced by the
    partial unique `org_memberships_one_owner_idx`
    (migration 00099). The new owner must already be an active
    member of the org.

    Args:
        slug (str):
        body (TransferOwnershipRequest): POST /v1/orgs/{slug}/transfer_ownership body. The new
            owner
            must already be an active member of the org; the previous
            owner becomes admin on success. The exactly-one-owner
            invariant is enforced by the partial unique index
            `org_memberships_one_owner_idx` (migration 00099).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgResponse | Problem]
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
    body: TransferOwnershipRequest,
) -> OrgResponse | Problem | None:
    """Transfer ownership to another active member.

     Atomically promotes `new_owner_account_id` to owner and
    demotes the caller to admin via `Store.TransferOrgOwnership`
    (a single PostgreSQL tx with `FOR UPDATE` locks on both
    rows). The exactly-one-owner invariant is enforced by the
    partial unique `org_memberships_one_owner_idx`
    (migration 00099). The new owner must already be an active
    member of the org.

    Args:
        slug (str):
        body (TransferOwnershipRequest): POST /v1/orgs/{slug}/transfer_ownership body. The new
            owner
            must already be an active member of the org; the previous
            owner becomes admin on success. The exactly-one-owner
            invariant is enforced by the partial unique index
            `org_memberships_one_owner_idx` (migration 00099).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgResponse | Problem
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
    body: TransferOwnershipRequest,
) -> Response[OrgResponse | Problem]:
    """Transfer ownership to another active member.

     Atomically promotes `new_owner_account_id` to owner and
    demotes the caller to admin via `Store.TransferOrgOwnership`
    (a single PostgreSQL tx with `FOR UPDATE` locks on both
    rows). The exactly-one-owner invariant is enforced by the
    partial unique `org_memberships_one_owner_idx`
    (migration 00099). The new owner must already be an active
    member of the org.

    Args:
        slug (str):
        body (TransferOwnershipRequest): POST /v1/orgs/{slug}/transfer_ownership body. The new
            owner
            must already be an active member of the org; the previous
            owner becomes admin on success. The exactly-one-owner
            invariant is enforced by the partial unique index
            `org_memberships_one_owner_idx` (migration 00099).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgResponse | Problem]
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
    body: TransferOwnershipRequest,
) -> OrgResponse | Problem | None:
    """Transfer ownership to another active member.

     Atomically promotes `new_owner_account_id` to owner and
    demotes the caller to admin via `Store.TransferOrgOwnership`
    (a single PostgreSQL tx with `FOR UPDATE` locks on both
    rows). The exactly-one-owner invariant is enforced by the
    partial unique `org_memberships_one_owner_idx`
    (migration 00099). The new owner must already be an active
    member of the org.

    Args:
        slug (str):
        body (TransferOwnershipRequest): POST /v1/orgs/{slug}/transfer_ownership body. The new
            owner
            must already be an active member of the org; the previous
            owner becomes admin on success. The exactly-one-owner
            invariant is enforced by the partial unique index
            `org_memberships_one_owner_idx` (migration 00099).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
