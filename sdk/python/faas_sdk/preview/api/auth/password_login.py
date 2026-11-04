from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.password_login_request import PasswordLoginRequest
from ...models.password_login_response import PasswordLoginResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: PasswordLoginRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/login",
    }

    if isinstance(body, PasswordLoginRequest):
        _kwargs["json"] = body.to_dict()

        headers["Content-Type"] = "application/json"
    if isinstance(body, PasswordLoginRequest):
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
    body: PasswordLoginRequest | Unset = UNSET,
) -> Response[PasswordLoginResponse | Problem]:
    """Email + password sign-in.

     Verifies the email + password against the `account_passwords`
    row (Argon2id, PHC string). Sets the `faas_sid` session
    cookie on success. The response body carries only
    `{account_id, plan}` — no API key. The SDK's device-code
    flow is the programmatic-auth path; the dashboard cookie is
    the only auth artifact on the browser side.

    Anti-enumeration: unbound email, wrong password, and
    passwordless (OAuth-only) accounts all return 401
    `invalid_credentials` with the same body. Every code path
    runs one Argon2id verify under identical parameters so the
    timing oracle stays closed.

    Args:
        body (PasswordLoginRequest): Email + password for sign-in. Email is canonicalised server-
            side (lowercase + trim).
        body (PasswordLoginRequest): Email + password for sign-in. Email is canonicalised server-
            side (lowercase + trim).

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
    body: PasswordLoginRequest | Unset = UNSET,
) -> PasswordLoginResponse | Problem | None:
    """Email + password sign-in.

     Verifies the email + password against the `account_passwords`
    row (Argon2id, PHC string). Sets the `faas_sid` session
    cookie on success. The response body carries only
    `{account_id, plan}` — no API key. The SDK's device-code
    flow is the programmatic-auth path; the dashboard cookie is
    the only auth artifact on the browser side.

    Anti-enumeration: unbound email, wrong password, and
    passwordless (OAuth-only) accounts all return 401
    `invalid_credentials` with the same body. Every code path
    runs one Argon2id verify under identical parameters so the
    timing oracle stays closed.

    Args:
        body (PasswordLoginRequest): Email + password for sign-in. Email is canonicalised server-
            side (lowercase + trim).
        body (PasswordLoginRequest): Email + password for sign-in. Email is canonicalised server-
            side (lowercase + trim).

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
    body: PasswordLoginRequest | Unset = UNSET,
) -> Response[PasswordLoginResponse | Problem]:
    """Email + password sign-in.

     Verifies the email + password against the `account_passwords`
    row (Argon2id, PHC string). Sets the `faas_sid` session
    cookie on success. The response body carries only
    `{account_id, plan}` — no API key. The SDK's device-code
    flow is the programmatic-auth path; the dashboard cookie is
    the only auth artifact on the browser side.

    Anti-enumeration: unbound email, wrong password, and
    passwordless (OAuth-only) accounts all return 401
    `invalid_credentials` with the same body. Every code path
    runs one Argon2id verify under identical parameters so the
    timing oracle stays closed.

    Args:
        body (PasswordLoginRequest): Email + password for sign-in. Email is canonicalised server-
            side (lowercase + trim).
        body (PasswordLoginRequest): Email + password for sign-in. Email is canonicalised server-
            side (lowercase + trim).

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
    body: PasswordLoginRequest | Unset = UNSET,
) -> PasswordLoginResponse | Problem | None:
    """Email + password sign-in.

     Verifies the email + password against the `account_passwords`
    row (Argon2id, PHC string). Sets the `faas_sid` session
    cookie on success. The response body carries only
    `{account_id, plan}` — no API key. The SDK's device-code
    flow is the programmatic-auth path; the dashboard cookie is
    the only auth artifact on the browser side.

    Anti-enumeration: unbound email, wrong password, and
    passwordless (OAuth-only) accounts all return 401
    `invalid_credentials` with the same body. Every code path
    runs one Argon2id verify under identical parameters so the
    timing oracle stays closed.

    Args:
        body (PasswordLoginRequest): Email + password for sign-in. Email is canonicalised server-
            side (lowercase + trim).
        body (PasswordLoginRequest): Email + password for sign-in. Email is canonicalised server-
            side (lowercase + trim).

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
