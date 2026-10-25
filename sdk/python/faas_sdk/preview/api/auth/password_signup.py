from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.password_login_response import PasswordLoginResponse
from ...models.password_signup_request import PasswordSignupRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: PasswordSignupRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/signup",
    }

    if isinstance(body, PasswordSignupRequest):
        _kwargs["json"] = body.to_dict()

        headers["Content-Type"] = "application/json"
    if isinstance(body, PasswordSignupRequest):
        _kwargs["data"] = body.to_dict()
        headers["Content-Type"] = "application/x-www-form-urlencoded"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PasswordLoginResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PasswordLoginResponse.from_dict(response.json())

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
) -> Response[PasswordLoginResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PasswordSignupRequest | Unset = UNSET,
) -> Response[PasswordLoginResponse | Problem]:
    """Create account with email + password.

     Creates an account and signs the caller in. On a colliding
    email + matching password, the call is idempotent and signs
    in. On a colliding email + different password, the response
    is 401 `invalid_credentials` (never 409 — the surface
    forbids account enumeration).

    Password floor: 12 characters (NIST-style; no complexity
    rules).

    Args:
        body (PasswordSignupRequest): Email + password for signup. Same shape as
            PasswordLoginRequest so the create-or-claim race detection reuses the verifier.
        body (PasswordSignupRequest): Email + password for signup. Same shape as
            PasswordLoginRequest so the create-or-claim race detection reuses the verifier.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PasswordLoginResponse | Problem]
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
    body: PasswordSignupRequest | Unset = UNSET,
) -> PasswordLoginResponse | Problem | None:
    """Create account with email + password.

     Creates an account and signs the caller in. On a colliding
    email + matching password, the call is idempotent and signs
    in. On a colliding email + different password, the response
    is 401 `invalid_credentials` (never 409 — the surface
    forbids account enumeration).

    Password floor: 12 characters (NIST-style; no complexity
    rules).

    Args:
        body (PasswordSignupRequest): Email + password for signup. Same shape as
            PasswordLoginRequest so the create-or-claim race detection reuses the verifier.
        body (PasswordSignupRequest): Email + password for signup. Same shape as
            PasswordLoginRequest so the create-or-claim race detection reuses the verifier.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PasswordLoginResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PasswordSignupRequest | Unset = UNSET,
) -> Response[PasswordLoginResponse | Problem]:
    """Create account with email + password.

     Creates an account and signs the caller in. On a colliding
    email + matching password, the call is idempotent and signs
    in. On a colliding email + different password, the response
    is 401 `invalid_credentials` (never 409 — the surface
    forbids account enumeration).

    Password floor: 12 characters (NIST-style; no complexity
    rules).

    Args:
        body (PasswordSignupRequest): Email + password for signup. Same shape as
            PasswordLoginRequest so the create-or-claim race detection reuses the verifier.
        body (PasswordSignupRequest): Email + password for signup. Same shape as
            PasswordLoginRequest so the create-or-claim race detection reuses the verifier.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PasswordLoginResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: PasswordSignupRequest | Unset = UNSET,
) -> PasswordLoginResponse | Problem | None:
    """Create account with email + password.

     Creates an account and signs the caller in. On a colliding
    email + matching password, the call is idempotent and signs
    in. On a colliding email + different password, the response
    is 401 `invalid_credentials` (never 409 — the surface
    forbids account enumeration).

    Password floor: 12 characters (NIST-style; no complexity
    rules).

    Args:
        body (PasswordSignupRequest): Email + password for signup. Same shape as
            PasswordLoginRequest so the create-or-claim race detection reuses the verifier.
        body (PasswordSignupRequest): Email + password for signup. Same shape as
            PasswordLoginRequest so the create-or-claim race detection reuses the verifier.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PasswordLoginResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
