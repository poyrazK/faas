from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.invitation_list_response import InvitationListResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    before: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["before"] = before

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{slug}/invitations".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> InvitationListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = InvitationListResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[InvitationListResponse | Problem]:
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
    before: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> Response[InvitationListResponse | Problem]:
    r"""List org invitations (every state).

     Cursor-paginated list of every invitation minted on the
    org — pending, consumed, revoked, expired — in
    `created_at DESC` order (id tiebreak). The cursor is a
    opaque (base64-url-of-JSON) compound key `(created_at,
    id)`; `?before=<cursor>` partitions the next page so
    the row at the boundary is visited exactly once even
    under random UUIDs (PR-9 cursor upgrade). Default limit
    25, max 100 (per the strict-mode pagination contract at
    issue #393). Every role may read (gated by `org.view`,
    the same access model as GET /v1/orgs/{slug}/members).
    PR-8 ships the surface so the dashboard can render a
    \"Pending invitations\" table next to the \"Members\"
    table. Each render emits one `org.invitation.viewed`
    audit row (success-only, per ADR-035).

    Args:
        slug (str):
        before (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InvitationListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        before=before,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> InvitationListResponse | Problem | None:
    r"""List org invitations (every state).

     Cursor-paginated list of every invitation minted on the
    org — pending, consumed, revoked, expired — in
    `created_at DESC` order (id tiebreak). The cursor is a
    opaque (base64-url-of-JSON) compound key `(created_at,
    id)`; `?before=<cursor>` partitions the next page so
    the row at the boundary is visited exactly once even
    under random UUIDs (PR-9 cursor upgrade). Default limit
    25, max 100 (per the strict-mode pagination contract at
    issue #393). Every role may read (gated by `org.view`,
    the same access model as GET /v1/orgs/{slug}/members).
    PR-8 ships the surface so the dashboard can render a
    \"Pending invitations\" table next to the \"Members\"
    table. Each render emits one `org.invitation.viewed`
    audit row (success-only, per ADR-035).

    Args:
        slug (str):
        before (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InvitationListResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        before=before,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> Response[InvitationListResponse | Problem]:
    r"""List org invitations (every state).

     Cursor-paginated list of every invitation minted on the
    org — pending, consumed, revoked, expired — in
    `created_at DESC` order (id tiebreak). The cursor is a
    opaque (base64-url-of-JSON) compound key `(created_at,
    id)`; `?before=<cursor>` partitions the next page so
    the row at the boundary is visited exactly once even
    under random UUIDs (PR-9 cursor upgrade). Default limit
    25, max 100 (per the strict-mode pagination contract at
    issue #393). Every role may read (gated by `org.view`,
    the same access model as GET /v1/orgs/{slug}/members).
    PR-8 ships the surface so the dashboard can render a
    \"Pending invitations\" table next to the \"Members\"
    table. Each render emits one `org.invitation.viewed`
    audit row (success-only, per ADR-035).

    Args:
        slug (str):
        before (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InvitationListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        before=before,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> InvitationListResponse | Problem | None:
    r"""List org invitations (every state).

     Cursor-paginated list of every invitation minted on the
    org — pending, consumed, revoked, expired — in
    `created_at DESC` order (id tiebreak). The cursor is a
    opaque (base64-url-of-JSON) compound key `(created_at,
    id)`; `?before=<cursor>` partitions the next page so
    the row at the boundary is visited exactly once even
    under random UUIDs (PR-9 cursor upgrade). Default limit
    25, max 100 (per the strict-mode pagination contract at
    issue #393). Every role may read (gated by `org.view`,
    the same access model as GET /v1/orgs/{slug}/members).
    PR-8 ships the surface so the dashboard can render a
    \"Pending invitations\" table next to the \"Members\"
    table. Each render emits one `org.invitation.viewed`
    audit row (success-only, per ADR-035).

    Args:
        slug (str):
        before (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InvitationListResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            before=before,
            limit=limit,
        )
    ).parsed
