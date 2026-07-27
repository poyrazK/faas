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
    """Set a password on the authenticated account.

     Authenticated. Lets OAuth-only customers opt into password
    login. The same Argon2id path as `/auth/reset`, anchored
    to the session's account rather than a reset token.

    Args:
        faas_sid (str | Unset):
        body (SetPasswordRequest): New password for the authenticated account. Lets OAuth-only
            customers opt into password login.

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
    """Set a password on the authenticated account.

     Authenticated. Lets OAuth-only customers opt into password
    login. The same Argon2id path as `/auth/reset`, anchored
    to the session's account rather than a reset token.

    Args:
        faas_sid (str | Unset):
        body (SetPasswordRequest): New password for the authenticated account. Lets OAuth-only
            customers opt into password login.

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
    """Set a password on the authenticated account.

     Authenticated. Lets OAuth-only customers opt into password
    login. The same Argon2id path as `/auth/reset`, anchored
    to the session's account rather than a reset token.

    Args:
        faas_sid (str | Unset):
        body (SetPasswordRequest): New password for the authenticated account. Lets OAuth-only
            customers opt into password login.

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
    """Set a password on the authenticated account.

     Authenticated. Lets OAuth-only customers opt into password
    login. The same Argon2id path as `/auth/reset`, anchored
    to the session's account rather than a reset token.

    Args:
        faas_sid (str | Unset):
        body (SetPasswordRequest): New password for the authenticated account. Lets OAuth-only
            customers opt into password login.

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
