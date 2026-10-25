from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.password_signup_request import PasswordSignupRequest
from ...models.problem import Problem
from ...models.programmatic_auth_response import ProgrammaticAuthResponse
from ...types import Response


def _get_kwargs(
    *,
    body: PasswordSignupRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/auth/signup",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProgrammaticAuthResponse | None:
    if response.status_code == 200:
        response_200 = ProgrammaticAuthResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ProgrammaticAuthResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PasswordSignupRequest,
) -> Response[Problem | ProgrammaticAuthResponse]:
    """Programmatic signup (JSON-only, bearer-key CLI path).

     Issue #311 / `gregale signup` — JSON-only endpoint that
    creates an account (or signs the caller in idempotently),
    mints a fresh programmatic API key, and returns the
    `ProgrammaticAuthResponse` payload. The CLI persists the
    plaintext via `saveToken()` without a dashboard round-trip.

    Anti-enumeration posture mirrors `/signup`:
      - email unbound: create + set password + mint key.
      - email bound + same password: idempotent sign-in + mint key.
      - email bound + different password: 401 `invalid_credentials`.
    No Set-Cookie header; bearer-key only.

    Args:
        body (PasswordSignupRequest): Email + password for signup. Same shape as
            PasswordLoginRequest so the create-or-claim race detection reuses the verifier.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProgrammaticAuthResponse]
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
    body: PasswordSignupRequest,
) -> Problem | ProgrammaticAuthResponse | None:
    """Programmatic signup (JSON-only, bearer-key CLI path).

     Issue #311 / `gregale signup` — JSON-only endpoint that
    creates an account (or signs the caller in idempotently),
    mints a fresh programmatic API key, and returns the
    `ProgrammaticAuthResponse` payload. The CLI persists the
    plaintext via `saveToken()` without a dashboard round-trip.

    Anti-enumeration posture mirrors `/signup`:
      - email unbound: create + set password + mint key.
      - email bound + same password: idempotent sign-in + mint key.
      - email bound + different password: 401 `invalid_credentials`.
    No Set-Cookie header; bearer-key only.

    Args:
        body (PasswordSignupRequest): Email + password for signup. Same shape as
            PasswordLoginRequest so the create-or-claim race detection reuses the verifier.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProgrammaticAuthResponse
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PasswordSignupRequest,
) -> Response[Problem | ProgrammaticAuthResponse]:
    """Programmatic signup (JSON-only, bearer-key CLI path).

     Issue #311 / `gregale signup` — JSON-only endpoint that
    creates an account (or signs the caller in idempotently),
    mints a fresh programmatic API key, and returns the
    `ProgrammaticAuthResponse` payload. The CLI persists the
    plaintext via `saveToken()` without a dashboard round-trip.

    Anti-enumeration posture mirrors `/signup`:
      - email unbound: create + set password + mint key.
      - email bound + same password: idempotent sign-in + mint key.
      - email bound + different password: 401 `invalid_credentials`.
    No Set-Cookie header; bearer-key only.

    Args:
        body (PasswordSignupRequest): Email + password for signup. Same shape as
            PasswordLoginRequest so the create-or-claim race detection reuses the verifier.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProgrammaticAuthResponse]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: PasswordSignupRequest,
) -> Problem | ProgrammaticAuthResponse | None:
    """Programmatic signup (JSON-only, bearer-key CLI path).

     Issue #311 / `gregale signup` — JSON-only endpoint that
    creates an account (or signs the caller in idempotently),
    mints a fresh programmatic API key, and returns the
    `ProgrammaticAuthResponse` payload. The CLI persists the
    plaintext via `saveToken()` without a dashboard round-trip.

    Anti-enumeration posture mirrors `/signup`:
      - email unbound: create + set password + mint key.
      - email bound + same password: idempotent sign-in + mint key.
      - email bound + different password: 401 `invalid_credentials`.
    No Set-Cookie header; bearer-key only.

    Args:
        body (PasswordSignupRequest): Email + password for signup. Same shape as
            PasswordLoginRequest so the create-or-claim race detection reuses the verifier.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProgrammaticAuthResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
