from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.magic_link_signup_request import MagicLinkSignupRequest
from ...models.problem import Problem
from ...models.programmatic_signup_magic_link_response_200 import ProgrammaticSignupMagicLinkResponse200
from ...types import Response


def _get_kwargs(
    *,
    body: MagicLinkSignupRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/auth/signup/magic-link",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProgrammaticSignupMagicLinkResponse200 | None:
    if response.status_code == 200:
        response_200 = ProgrammaticSignupMagicLinkResponse200.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ProgrammaticSignupMagicLinkResponse200]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MagicLinkSignupRequest,
) -> Response[Problem | ProgrammaticSignupMagicLinkResponse200]:
    """Magic-link signup (JSON-only, no password).

     Issue #311 / `gregale signup --email-only EMAIL` — emails
    a one-time signup link to the given address. Always
    returns 200 with the same body regardless of whether the
    email is bound, unbound, malformed, or missing in the
    request — the response cannot be used to enumerate
    accounts.

    On a real-account hit, the handler creates the account if
    unbound, mints a 32-byte token, persists its SHA-256 via
    `IssueLoginToken` (15-minute TTL), and emails the
    `/auth/verify?token=...` link through the platform mailer.

    Args:
        body (MagicLinkSignupRequest): Email for the magic-link signup path. Optional; the
            handler accepts a missing or unparseable email and still
            returns 200 so the response cannot be used to enumerate
            accounts.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProgrammaticSignupMagicLinkResponse200]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: MagicLinkSignupRequest,
) -> Problem | ProgrammaticSignupMagicLinkResponse200 | None:
    """Magic-link signup (JSON-only, no password).

     Issue #311 / `gregale signup --email-only EMAIL` — emails
    a one-time signup link to the given address. Always
    returns 200 with the same body regardless of whether the
    email is bound, unbound, malformed, or missing in the
    request — the response cannot be used to enumerate
    accounts.

    On a real-account hit, the handler creates the account if
    unbound, mints a 32-byte token, persists its SHA-256 via
    `IssueLoginToken` (15-minute TTL), and emails the
    `/auth/verify?token=...` link through the platform mailer.

    Args:
        body (MagicLinkSignupRequest): Email for the magic-link signup path. Optional; the
            handler accepts a missing or unparseable email and still
            returns 200 so the response cannot be used to enumerate
            accounts.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProgrammaticSignupMagicLinkResponse200
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MagicLinkSignupRequest,
) -> Response[Problem | ProgrammaticSignupMagicLinkResponse200]:
    """Magic-link signup (JSON-only, no password).

     Issue #311 / `gregale signup --email-only EMAIL` — emails
    a one-time signup link to the given address. Always
    returns 200 with the same body regardless of whether the
    email is bound, unbound, malformed, or missing in the
    request — the response cannot be used to enumerate
    accounts.

    On a real-account hit, the handler creates the account if
    unbound, mints a 32-byte token, persists its SHA-256 via
    `IssueLoginToken` (15-minute TTL), and emails the
    `/auth/verify?token=...` link through the platform mailer.

    Args:
        body (MagicLinkSignupRequest): Email for the magic-link signup path. Optional; the
            handler accepts a missing or unparseable email and still
            returns 200 so the response cannot be used to enumerate
            accounts.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProgrammaticSignupMagicLinkResponse200]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: MagicLinkSignupRequest,
) -> Problem | ProgrammaticSignupMagicLinkResponse200 | None:
    """Magic-link signup (JSON-only, no password).

     Issue #311 / `gregale signup --email-only EMAIL` — emails
    a one-time signup link to the given address. Always
    returns 200 with the same body regardless of whether the
    email is bound, unbound, malformed, or missing in the
    request — the response cannot be used to enumerate
    accounts.

    On a real-account hit, the handler creates the account if
    unbound, mints a 32-byte token, persists its SHA-256 via
    `IssueLoginToken` (15-minute TTL), and emails the
    `/auth/verify?token=...` link through the platform mailer.

    Args:
        body (MagicLinkSignupRequest): Email for the magic-link signup path. Optional; the
            handler accepts a missing or unparseable email and still
            returns 200 so the response cannot be used to enumerate
            accounts.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProgrammaticSignupMagicLinkResponse200
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
