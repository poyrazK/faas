from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.set_password_request import SetPasswordRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: SetPasswordRequest,
    faas_sid: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    cookies = {}
    if faas_sid is not UNSET:
        cookies["faas_sid"] = faas_sid

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/dashboard/account/set-password",
        "cookies": cookies,
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

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
    body: SetPasswordRequest,
    faas_sid: str | Unset = UNSET,
) -> Response[Any | Problem]:
    """Set or replace the password on the authenticated account.

     Authenticated. Lets OAuth-only customers opt into password
    login and lets customers who already have a password replace
    it. The same Argon2id path as `/auth/reset`, anchored to the
    session's account rather than a reset token.

    The form must carry a `csrf_token` minted by
    `GET /v1/auth/csrf?action=set_password` (double-submit with
    the `faas_csrf` cookie); a missing or mismatched token is 400
    `validation_failed`. The route is a same-site form POST, so
    `SameSite=Lax` alone does not protect it from a customer app
    hosted under `*.apps.gregale.dev`.

    Proof of presence (ADR-140), decided by what the account has:

    - A step-up verified within the last 5 minutes
      (`POST /v1/account/mfa/verify`) is accepted as-is.
    - Otherwise, if an explicit `mfa_required` policy is armed
      while the account has not enrolled, 403 `mfa_required`.
      This is a policy hook; MFA remains opt-in for ordinary
      accounts.
    - Otherwise, if the account has MFA enrolled, 403
      `step_up_required` — verify TOTP first, whether or not the
      account also has a password.
    - Otherwise, if the account already has a password,
      `current_password` is required and verified. Missing and
      wrong both answer 401 `invalid_credentials`.
    - Otherwise (OAuth-only, no MFA) the request is accepted; the
      session is the only proof the account has.

    Args:
        faas_sid (str | Unset):
        body (SetPasswordRequest): New password for the authenticated account. Lets OAuth-only
            customers opt into password login, and customers who already have a password replace it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        faas_sid=faas_sid,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: SetPasswordRequest,
    faas_sid: str | Unset = UNSET,
) -> Any | Problem | None:
    """Set or replace the password on the authenticated account.

     Authenticated. Lets OAuth-only customers opt into password
    login and lets customers who already have a password replace
    it. The same Argon2id path as `/auth/reset`, anchored to the
    session's account rather than a reset token.

    The form must carry a `csrf_token` minted by
    `GET /v1/auth/csrf?action=set_password` (double-submit with
    the `faas_csrf` cookie); a missing or mismatched token is 400
    `validation_failed`. The route is a same-site form POST, so
    `SameSite=Lax` alone does not protect it from a customer app
    hosted under `*.apps.gregale.dev`.

    Proof of presence (ADR-140), decided by what the account has:

    - A step-up verified within the last 5 minutes
      (`POST /v1/account/mfa/verify`) is accepted as-is.
    - Otherwise, if an explicit `mfa_required` policy is armed
      while the account has not enrolled, 403 `mfa_required`.
      This is a policy hook; MFA remains opt-in for ordinary
      accounts.
    - Otherwise, if the account has MFA enrolled, 403
      `step_up_required` — verify TOTP first, whether or not the
      account also has a password.
    - Otherwise, if the account already has a password,
      `current_password` is required and verified. Missing and
      wrong both answer 401 `invalid_credentials`.
    - Otherwise (OAuth-only, no MFA) the request is accepted; the
      session is the only proof the account has.

    Args:
        faas_sid (str | Unset):
        body (SetPasswordRequest): New password for the authenticated account. Lets OAuth-only
            customers opt into password login, and customers who already have a password replace it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        faas_sid=faas_sid,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: SetPasswordRequest,
    faas_sid: str | Unset = UNSET,
) -> Response[Any | Problem]:
    """Set or replace the password on the authenticated account.

     Authenticated. Lets OAuth-only customers opt into password
    login and lets customers who already have a password replace
    it. The same Argon2id path as `/auth/reset`, anchored to the
    session's account rather than a reset token.

    The form must carry a `csrf_token` minted by
    `GET /v1/auth/csrf?action=set_password` (double-submit with
    the `faas_csrf` cookie); a missing or mismatched token is 400
    `validation_failed`. The route is a same-site form POST, so
    `SameSite=Lax` alone does not protect it from a customer app
    hosted under `*.apps.gregale.dev`.

    Proof of presence (ADR-140), decided by what the account has:

    - A step-up verified within the last 5 minutes
      (`POST /v1/account/mfa/verify`) is accepted as-is.
    - Otherwise, if an explicit `mfa_required` policy is armed
      while the account has not enrolled, 403 `mfa_required`.
      This is a policy hook; MFA remains opt-in for ordinary
      accounts.
    - Otherwise, if the account has MFA enrolled, 403
      `step_up_required` — verify TOTP first, whether or not the
      account also has a password.
    - Otherwise, if the account already has a password,
      `current_password` is required and verified. Missing and
      wrong both answer 401 `invalid_credentials`.
    - Otherwise (OAuth-only, no MFA) the request is accepted; the
      session is the only proof the account has.

    Args:
        faas_sid (str | Unset):
        body (SetPasswordRequest): New password for the authenticated account. Lets OAuth-only
            customers opt into password login, and customers who already have a password replace it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        faas_sid=faas_sid,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: SetPasswordRequest,
    faas_sid: str | Unset = UNSET,
) -> Any | Problem | None:
    """Set or replace the password on the authenticated account.

     Authenticated. Lets OAuth-only customers opt into password
    login and lets customers who already have a password replace
    it. The same Argon2id path as `/auth/reset`, anchored to the
    session's account rather than a reset token.

    The form must carry a `csrf_token` minted by
    `GET /v1/auth/csrf?action=set_password` (double-submit with
    the `faas_csrf` cookie); a missing or mismatched token is 400
    `validation_failed`. The route is a same-site form POST, so
    `SameSite=Lax` alone does not protect it from a customer app
    hosted under `*.apps.gregale.dev`.

    Proof of presence (ADR-140), decided by what the account has:

    - A step-up verified within the last 5 minutes
      (`POST /v1/account/mfa/verify`) is accepted as-is.
    - Otherwise, if an explicit `mfa_required` policy is armed
      while the account has not enrolled, 403 `mfa_required`.
      This is a policy hook; MFA remains opt-in for ordinary
      accounts.
    - Otherwise, if the account has MFA enrolled, 403
      `step_up_required` — verify TOTP first, whether or not the
      account also has a password.
    - Otherwise, if the account already has a password,
      `current_password` is required and verified. Missing and
      wrong both answer 401 `invalid_credentials`.
    - Otherwise (OAuth-only, no MFA) the request is accepted; the
      session is the only proof the account has.

    Args:
        faas_sid (str | Unset):
        body (SetPasswordRequest): New password for the authenticated account. Lets OAuth-only
            customers opt into password login, and customers who already have a password replace it.

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
            faas_sid=faas_sid,
        )
    ).parsed
