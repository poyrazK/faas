from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.password_forgot_response_200 import PasswordForgotResponse200
from ...models.password_reset_request import PasswordResetRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: PasswordResetRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/login/forgot",
    }

    if isinstance(body, PasswordResetRequest):
        if not isinstance(body, Unset):
            _kwargs["json"] = body.to_dict()

        headers["Content-Type"] = "application/json"
    if isinstance(body, PasswordResetRequest):
        if not isinstance(body, Unset):
            _kwargs["data"] = body.to_dict()
        headers["Content-Type"] = "application/x-www-form-urlencoded"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PasswordForgotResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = PasswordForgotResponse200.from_dict(response.json())

        return response_200

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PasswordForgotResponse200 | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PasswordResetRequest | Unset = UNSET,
) -> Response[PasswordForgotResponse200 | Problem]:
    """Request a password-reset email.

     Always returns 200 with the same body regardless of
    whether the email is bound to an account. The reset URL
    is mailed via the platform's outbound mailer; the response
    never leaks account presence.

    Args:
        body (PasswordResetRequest | Unset): Email for a password-reset request. Optional — the
            form-page path submits no body; the SDK sends the email.
        body (PasswordResetRequest | Unset): Email for a password-reset request. Optional — the
            form-page path submits no body; the SDK sends the email.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PasswordForgotResponse200 | Problem]
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
    body: PasswordResetRequest | Unset = UNSET,
) -> PasswordForgotResponse200 | Problem | None:
    """Request a password-reset email.

     Always returns 200 with the same body regardless of
    whether the email is bound to an account. The reset URL
    is mailed via the platform's outbound mailer; the response
    never leaks account presence.

    Args:
        body (PasswordResetRequest | Unset): Email for a password-reset request. Optional — the
            form-page path submits no body; the SDK sends the email.
        body (PasswordResetRequest | Unset): Email for a password-reset request. Optional — the
            form-page path submits no body; the SDK sends the email.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PasswordForgotResponse200 | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PasswordResetRequest | Unset = UNSET,
) -> Response[PasswordForgotResponse200 | Problem]:
    """Request a password-reset email.

     Always returns 200 with the same body regardless of
    whether the email is bound to an account. The reset URL
    is mailed via the platform's outbound mailer; the response
    never leaks account presence.

    Args:
        body (PasswordResetRequest | Unset): Email for a password-reset request. Optional — the
            form-page path submits no body; the SDK sends the email.
        body (PasswordResetRequest | Unset): Email for a password-reset request. Optional — the
            form-page path submits no body; the SDK sends the email.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PasswordForgotResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: PasswordResetRequest | Unset = UNSET,
) -> PasswordForgotResponse200 | Problem | None:
    """Request a password-reset email.

     Always returns 200 with the same body regardless of
    whether the email is bound to an account. The reset URL
    is mailed via the platform's outbound mailer; the response
    never leaks account presence.

    Args:
        body (PasswordResetRequest | Unset): Email for a password-reset request. Optional — the
            form-page path submits no body; the SDK sends the email.
        body (PasswordResetRequest | Unset): Email for a password-reset request. Optional — the
            form-page path submits no body; the SDK sends the email.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PasswordForgotResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
