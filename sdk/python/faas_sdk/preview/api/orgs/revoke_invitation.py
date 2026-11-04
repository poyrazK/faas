from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    invitation_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/orgs/{slug}/invitations/{invitation_id}".format(
            slug=quote(str(slug), safe=""),
            invitation_id=quote(str(invitation_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    invitation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Revoke a pending invitation.

     Stamps `revoked_at` on a still-pending invitation via
    `Store.RevokeOrgInvitation`. Owner + admin only
    (`org.invite_members`, symmetric with the create-invite
    path). Already-consumed / already-revoked / unknown IDs
    collapse to a single `org_invitation_invalid` 410 (don't
    leak which row state was reached). Emits
    `org.invitation.revoked` with the stable invitation ID for dashboard
    correlation. Plaintext tokens and hashes are never written to audit.

    Args:
        slug (str):
        invitation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        invitation_id=invitation_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    invitation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Revoke a pending invitation.

     Stamps `revoked_at` on a still-pending invitation via
    `Store.RevokeOrgInvitation`. Owner + admin only
    (`org.invite_members`, symmetric with the create-invite
    path). Already-consumed / already-revoked / unknown IDs
    collapse to a single `org_invitation_invalid` 410 (don't
    leak which row state was reached). Emits
    `org.invitation.revoked` with the stable invitation ID for dashboard
    correlation. Plaintext tokens and hashes are never written to audit.

    Args:
        slug (str):
        invitation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        invitation_id=invitation_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    invitation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Revoke a pending invitation.

     Stamps `revoked_at` on a still-pending invitation via
    `Store.RevokeOrgInvitation`. Owner + admin only
    (`org.invite_members`, symmetric with the create-invite
    path). Already-consumed / already-revoked / unknown IDs
    collapse to a single `org_invitation_invalid` 410 (don't
    leak which row state was reached). Emits
    `org.invitation.revoked` with the stable invitation ID for dashboard
    correlation. Plaintext tokens and hashes are never written to audit.

    Args:
        slug (str):
        invitation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        invitation_id=invitation_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    invitation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Revoke a pending invitation.

     Stamps `revoked_at` on a still-pending invitation via
    `Store.RevokeOrgInvitation`. Owner + admin only
    (`org.invite_members`, symmetric with the create-invite
    path). Already-consumed / already-revoked / unknown IDs
    collapse to a single `org_invitation_invalid` 410 (don't
    leak which row state was reached). Emits
    `org.invitation.revoked` with the stable invitation ID for dashboard
    correlation. Plaintext tokens and hashes are never written to audit.

    Args:
        slug (str):
        invitation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            invitation_id=invitation_id,
            client=client,
        )
    ).parsed
