from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.password_reset_confirm import PasswordResetConfirm
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: PasswordResetConfirm,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/auth/reset",
    }

    _kwargs["data"] = body.to_dict()
    headers["Content-Type"] = "application/x-www-form-urlencoded"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 302:
        response_302 = cast(Any, None)
        return response_302

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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
    *,
    client: AuthenticatedClient | Client,
    body: PasswordResetConfirm,
) -> Response[Any | Problem]:
    """Submit a new password against a reset token.

     Atomically consumes the token (one-shot, replay returns
    410), Argon2id-encodes the new password, sets it on the
    account, and signs the caller in.

    Args:
        body (PasswordResetConfirm): Token + new password for the password-reset submission.
            Token is the base64url-encoded 32-byte value from the
            email link (NOT the SHA-256 hash the server stores).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
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
    body: PasswordResetConfirm,
) -> Any | Problem | None:
    """Submit a new password against a reset token.

     Atomically consumes the token (one-shot, replay returns
    410), Argon2id-encodes the new password, sets it on the
    account, and signs the caller in.

    Args:
        body (PasswordResetConfirm): Token + new password for the password-reset submission.
            Token is the base64url-encoded 32-byte value from the
            email link (NOT the SHA-256 hash the server stores).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PasswordResetConfirm,
) -> Response[Any | Problem]:
    """Submit a new password against a reset token.

     Atomically consumes the token (one-shot, replay returns
    410), Argon2id-encodes the new password, sets it on the
    account, and signs the caller in.

    Args:
        body (PasswordResetConfirm): Token + new password for the password-reset submission.
            Token is the base64url-encoded 32-byte value from the
            email link (NOT the SHA-256 hash the server stores).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: PasswordResetConfirm,
) -> Any | Problem | None:
    """Submit a new password against a reset token.

     Atomically consumes the token (one-shot, replay returns
    410), Argon2id-encodes the new password, sets it on the
    account, and signs the caller in.

    Args:
        body (PasswordResetConfirm): Token + new password for the password-reset submission.
            Token is the base64url-encoded 32-byte value from the
            email link (NOT the SHA-256 hash the server stores).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
