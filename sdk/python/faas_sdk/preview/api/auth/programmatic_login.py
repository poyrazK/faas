from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.password_login_request import PasswordLoginRequest
from ...models.problem import Problem
from ...models.programmatic_auth_response import ProgrammaticAuthResponse
from ...types import Response


def _get_kwargs(
    *,
    body: PasswordLoginRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/auth/login",
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
    body: PasswordLoginRequest,
) -> Response[Problem | ProgrammaticAuthResponse]:
    """Programmatic login (JSON-only, bearer-key CLI path).

     Issue #311 / `gregale login` mirror — JSON-only endpoint
    that authenticates an email + password and returns a
    `ProgrammaticAuthResponse` payload. Same response shape as
    `/v1/auth/signup` so the CLI can reuse its unmarshaler.

    Anti-enumeration posture mirrors `/login`: Argon2id pad
    on the no-row branch, identical 401 on wrong-password vs
    unbound email.

    Args:
        body (PasswordLoginRequest): Email + password for sign-in. Email is canonicalised server-
            side (lowercase + trim).

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
    body: PasswordLoginRequest,
) -> Problem | ProgrammaticAuthResponse | None:
    """Programmatic login (JSON-only, bearer-key CLI path).

     Issue #311 / `gregale login` mirror — JSON-only endpoint
    that authenticates an email + password and returns a
    `ProgrammaticAuthResponse` payload. Same response shape as
    `/v1/auth/signup` so the CLI can reuse its unmarshaler.

    Anti-enumeration posture mirrors `/login`: Argon2id pad
    on the no-row branch, identical 401 on wrong-password vs
    unbound email.

    Args:
        body (PasswordLoginRequest): Email + password for sign-in. Email is canonicalised server-
            side (lowercase + trim).

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
    body: PasswordLoginRequest,
) -> Response[Problem | ProgrammaticAuthResponse]:
    """Programmatic login (JSON-only, bearer-key CLI path).

     Issue #311 / `gregale login` mirror — JSON-only endpoint
    that authenticates an email + password and returns a
    `ProgrammaticAuthResponse` payload. Same response shape as
    `/v1/auth/signup` so the CLI can reuse its unmarshaler.

    Anti-enumeration posture mirrors `/login`: Argon2id pad
    on the no-row branch, identical 401 on wrong-password vs
    unbound email.

    Args:
        body (PasswordLoginRequest): Email + password for sign-in. Email is canonicalised server-
            side (lowercase + trim).

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
    body: PasswordLoginRequest,
) -> Problem | ProgrammaticAuthResponse | None:
    """Programmatic login (JSON-only, bearer-key CLI path).

     Issue #311 / `gregale login` mirror — JSON-only endpoint
    that authenticates an email + password and returns a
    `ProgrammaticAuthResponse` payload. Same response shape as
    `/v1/auth/signup` so the CLI can reuse its unmarshaler.

    Anti-enumeration posture mirrors `/login`: Argon2id pad
    on the no-row branch, identical 401 on wrong-password vs
    unbound email.

    Args:
        body (PasswordLoginRequest): Email + password for sign-in. Email is canonicalised server-
            side (lowercase + trim).

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
