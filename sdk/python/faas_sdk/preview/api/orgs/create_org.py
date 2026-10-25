from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_org_request import CreateOrgRequest
from ...models.org_response import OrgResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: CreateOrgRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> OrgResponse | Problem | None:
    if response.status_code == 201:
        response_201 = OrgResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[OrgResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateOrgRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[OrgResponse | Problem]:
    """Create a shared org (caller becomes the first owner).

     Mints a new shared (non-personal) org + the caller's owner
    membership in one transaction. The slug must match
    `^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$` (3..32 chars); name is
    trimmed-non-empty (1..256 chars). Personal orgs cannot be
    created via this endpoint — every account already has a
    personal org (PR 3 backfill / migration 00099).

    Args:
        idempotency_key (str | Unset):
        body (CreateOrgRequest): POST /v1/orgs body. Slug matches `OrgSlugPattern`
            (lowercase alphanumeric + dashes, 3..32 chars); name is
            trimmed-non-empty, capped at 256 chars. Personal orgs
            cannot be created via this endpoint — every account
            already owns an immutable personal org (PR 3 backfill).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: CreateOrgRequest,
    idempotency_key: str | Unset = UNSET,
) -> OrgResponse | Problem | None:
    """Create a shared org (caller becomes the first owner).

     Mints a new shared (non-personal) org + the caller's owner
    membership in one transaction. The slug must match
    `^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$` (3..32 chars); name is
    trimmed-non-empty (1..256 chars). Personal orgs cannot be
    created via this endpoint — every account already has a
    personal org (PR 3 backfill / migration 00099).

    Args:
        idempotency_key (str | Unset):
        body (CreateOrgRequest): POST /v1/orgs body. Slug matches `OrgSlugPattern`
            (lowercase alphanumeric + dashes, 3..32 chars); name is
            trimmed-non-empty, capped at 256 chars. Personal orgs
            cannot be created via this endpoint — every account
            already owns an immutable personal org (PR 3 backfill).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateOrgRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[OrgResponse | Problem]:
    """Create a shared org (caller becomes the first owner).

     Mints a new shared (non-personal) org + the caller's owner
    membership in one transaction. The slug must match
    `^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$` (3..32 chars); name is
    trimmed-non-empty (1..256 chars). Personal orgs cannot be
    created via this endpoint — every account already has a
    personal org (PR 3 backfill / migration 00099).

    Args:
        idempotency_key (str | Unset):
        body (CreateOrgRequest): POST /v1/orgs body. Slug matches `OrgSlugPattern`
            (lowercase alphanumeric + dashes, 3..32 chars); name is
            trimmed-non-empty, capped at 256 chars. Personal orgs
            cannot be created via this endpoint — every account
            already owns an immutable personal org (PR 3 backfill).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreateOrgRequest,
    idempotency_key: str | Unset = UNSET,
) -> OrgResponse | Problem | None:
    """Create a shared org (caller becomes the first owner).

     Mints a new shared (non-personal) org + the caller's owner
    membership in one transaction. The slug must match
    `^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$` (3..32 chars); name is
    trimmed-non-empty (1..256 chars). Personal orgs cannot be
    created via this endpoint — every account already has a
    personal org (PR 3 backfill / migration 00099).

    Args:
        idempotency_key (str | Unset):
        body (CreateOrgRequest): POST /v1/orgs body. Slug matches `OrgSlugPattern`
            (lowercase alphanumeric + dashes, 3..32 chars); name is
            trimmed-non-empty, capped at 256 chars. Personal orgs
            cannot be created via this endpoint — every account
            already owns an immutable personal org (PR 3 backfill).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
