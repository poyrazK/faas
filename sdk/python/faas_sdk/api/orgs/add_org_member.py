from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.invitation_with_token_response import InvitationWithTokenResponse
from ...models.invite_member_request import InviteMemberRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: InviteMemberRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{slug}/members".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> InvitationWithTokenResponse | Problem | None:
    if response.status_code == 201:
        response_201 = InvitationWithTokenResponse.from_dict(response.json())

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
) -> Response[InvitationWithTokenResponse | Problem]:
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
    body: InviteMemberRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[InvitationWithTokenResponse | Problem]:
    """Invite a new member (returns plaintext token ONCE).

     Mints a 32-byte plaintext token, hashes it via SHA-256 for
    storage, and returns the plaintext ONCE in the response.
    The token expires after 14 days; admins can revoke earlier by
    row ID via `DELETE /v1/orgs/{slug}/invitations/{invitation_id}` (PR 7 owns
    the accept surface too — see
    `POST /v1/invitations/{token}/accept`). Role cannot be
    `owner`; transfer-ownership is the only path to owner.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (InviteMemberRequest): POST /v1/orgs/{slug}/members body. Role cannot be `owner`.
            The handler mints a 32-byte plaintext token (returned ONCE
            in the response) and stores only the SHA-256 hash. The
            token expires after 14 days.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InvitationWithTokenResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: InviteMemberRequest,
    idempotency_key: str | Unset = UNSET,
) -> InvitationWithTokenResponse | Problem | None:
    """Invite a new member (returns plaintext token ONCE).

     Mints a 32-byte plaintext token, hashes it via SHA-256 for
    storage, and returns the plaintext ONCE in the response.
    The token expires after 14 days; admins can revoke earlier by
    row ID via `DELETE /v1/orgs/{slug}/invitations/{invitation_id}` (PR 7 owns
    the accept surface too — see
    `POST /v1/invitations/{token}/accept`). Role cannot be
    `owner`; transfer-ownership is the only path to owner.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (InviteMemberRequest): POST /v1/orgs/{slug}/members body. Role cannot be `owner`.
            The handler mints a 32-byte plaintext token (returned ONCE
            in the response) and stores only the SHA-256 hash. The
            token expires after 14 days.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InvitationWithTokenResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: InviteMemberRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[InvitationWithTokenResponse | Problem]:
    """Invite a new member (returns plaintext token ONCE).

     Mints a 32-byte plaintext token, hashes it via SHA-256 for
    storage, and returns the plaintext ONCE in the response.
    The token expires after 14 days; admins can revoke earlier by
    row ID via `DELETE /v1/orgs/{slug}/invitations/{invitation_id}` (PR 7 owns
    the accept surface too — see
    `POST /v1/invitations/{token}/accept`). Role cannot be
    `owner`; transfer-ownership is the only path to owner.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (InviteMemberRequest): POST /v1/orgs/{slug}/members body. Role cannot be `owner`.
            The handler mints a 32-byte plaintext token (returned ONCE
            in the response) and stores only the SHA-256 hash. The
            token expires after 14 days.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InvitationWithTokenResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: InviteMemberRequest,
    idempotency_key: str | Unset = UNSET,
) -> InvitationWithTokenResponse | Problem | None:
    """Invite a new member (returns plaintext token ONCE).

     Mints a 32-byte plaintext token, hashes it via SHA-256 for
    storage, and returns the plaintext ONCE in the response.
    The token expires after 14 days; admins can revoke earlier by
    row ID via `DELETE /v1/orgs/{slug}/invitations/{invitation_id}` (PR 7 owns
    the accept surface too — see
    `POST /v1/invitations/{token}/accept`). Role cannot be
    `owner`; transfer-ownership is the only path to owner.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (InviteMemberRequest): POST /v1/orgs/{slug}/members body. Role cannot be `owner`.
            The handler mints a 32-byte plaintext token (returned ONCE
            in the response) and stores only the SHA-256 hash. The
            token expires after 14 days.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InvitationWithTokenResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
