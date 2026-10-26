from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.org_invitation_response import OrgInvitationResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    token: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/invitations/{token}".format(
            token=quote(str(token), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OrgInvitationResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OrgInvitationResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OrgInvitationResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    token: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OrgInvitationResponse | Problem]:
    r"""Peek at a pending invitation by token (no consumption).

     Read-only lookup that returns the invitation metadata
    (email, role, org slug, expires_at) without consuming the
    token. Used by the dashboard to render \"you've been invited
    to Acme Inc. as developer\" without forcing the invitee
    to accept yet. PR 7 added the accept surface at
    `POST /v1/invitations/{token}/accept`.

    Args:
        token (str): Plaintext or base64url-encoded invitation token.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgInvitationResponse | Problem]
    """

    kwargs = _get_kwargs(
        token=token,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    token: str,
    *,
    client: AuthenticatedClient | Client,
) -> OrgInvitationResponse | Problem | None:
    r"""Peek at a pending invitation by token (no consumption).

     Read-only lookup that returns the invitation metadata
    (email, role, org slug, expires_at) without consuming the
    token. Used by the dashboard to render \"you've been invited
    to Acme Inc. as developer\" without forcing the invitee
    to accept yet. PR 7 added the accept surface at
    `POST /v1/invitations/{token}/accept`.

    Args:
        token (str): Plaintext or base64url-encoded invitation token.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgInvitationResponse | Problem
    """

    return sync_detailed(
        token=token,
        client=client,
    ).parsed


async def asyncio_detailed(
    token: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OrgInvitationResponse | Problem]:
    r"""Peek at a pending invitation by token (no consumption).

     Read-only lookup that returns the invitation metadata
    (email, role, org slug, expires_at) without consuming the
    token. Used by the dashboard to render \"you've been invited
    to Acme Inc. as developer\" without forcing the invitee
    to accept yet. PR 7 added the accept surface at
    `POST /v1/invitations/{token}/accept`.

    Args:
        token (str): Plaintext or base64url-encoded invitation token.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgInvitationResponse | Problem]
    """

    kwargs = _get_kwargs(
        token=token,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    token: str,
    *,
    client: AuthenticatedClient | Client,
) -> OrgInvitationResponse | Problem | None:
    r"""Peek at a pending invitation by token (no consumption).

     Read-only lookup that returns the invitation metadata
    (email, role, org slug, expires_at) without consuming the
    token. Used by the dashboard to render \"you've been invited
    to Acme Inc. as developer\" without forcing the invitee
    to accept yet. PR 7 added the accept surface at
    `POST /v1/invitations/{token}/accept`.

    Args:
        token (str): Plaintext or base64url-encoded invitation token.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgInvitationResponse | Problem
    """

    return (
        await asyncio_detailed(
            token=token,
            client=client,
        )
    ).parsed
