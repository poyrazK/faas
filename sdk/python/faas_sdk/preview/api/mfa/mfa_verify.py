from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mfa_verify_request import MFAVerifyRequest
from ...models.mfa_verify_response import MFAVerifyResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: MFAVerifyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/mfa/verify",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MFAVerifyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = MFAVerifyResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[MFAVerifyResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MFAVerifyRequest,
) -> Response[MFAVerifyResponse | Problem]:
    """Step up an mfa_pending session.

     For already-enrolled customers whose session cookie is
    mfa_pending. Same TOTP check as /confirm, but does NOT
    stamp mfa_enrolled_at. Re-issues the cookie without
    mfa_pending so subsequent requests pass requireMFA.

    Args:
        body (MFAVerifyRequest): Body for /verify — same 6-digit code as /confirm. The
            account is already enrolled; the verify path does NOT re-
            stamp enrolled_at, only re-issues the cookie without
            mfa_pending.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MFAVerifyResponse | Problem]
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
    body: MFAVerifyRequest,
) -> MFAVerifyResponse | Problem | None:
    """Step up an mfa_pending session.

     For already-enrolled customers whose session cookie is
    mfa_pending. Same TOTP check as /confirm, but does NOT
    stamp mfa_enrolled_at. Re-issues the cookie without
    mfa_pending so subsequent requests pass requireMFA.

    Args:
        body (MFAVerifyRequest): Body for /verify — same 6-digit code as /confirm. The
            account is already enrolled; the verify path does NOT re-
            stamp enrolled_at, only re-issues the cookie without
            mfa_pending.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MFAVerifyResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MFAVerifyRequest,
) -> Response[MFAVerifyResponse | Problem]:
    """Step up an mfa_pending session.

     For already-enrolled customers whose session cookie is
    mfa_pending. Same TOTP check as /confirm, but does NOT
    stamp mfa_enrolled_at. Re-issues the cookie without
    mfa_pending so subsequent requests pass requireMFA.

    Args:
        body (MFAVerifyRequest): Body for /verify — same 6-digit code as /confirm. The
            account is already enrolled; the verify path does NOT re-
            stamp enrolled_at, only re-issues the cookie without
            mfa_pending.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MFAVerifyResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: MFAVerifyRequest,
) -> MFAVerifyResponse | Problem | None:
    """Step up an mfa_pending session.

     For already-enrolled customers whose session cookie is
    mfa_pending. Same TOTP check as /confirm, but does NOT
    stamp mfa_enrolled_at. Re-issues the cookie without
    mfa_pending so subsequent requests pass requireMFA.

    Args:
        body (MFAVerifyRequest): Body for /verify — same 6-digit code as /confirm. The
            account is already enrolled; the verify path does NOT re-
            stamp enrolled_at, only re-issues the cookie without
            mfa_pending.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MFAVerifyResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
