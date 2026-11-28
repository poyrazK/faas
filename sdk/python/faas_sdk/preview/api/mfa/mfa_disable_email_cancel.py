from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mfa_disable_email_cancel_request import MFADisableEmailCancelRequest
from ...models.mfa_disable_email_cancel_response import MFADisableEmailCancelResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: MFADisableEmailCancelRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/mfa/disable-email-cancel",
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MFADisableEmailCancelResponse | Problem | None:
    if response.status_code == 200:
        response_200 = MFADisableEmailCancelResponse.from_dict(response.json())

        return response_200

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[MFADisableEmailCancelResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MFADisableEmailCancelRequest | Unset = UNSET,
) -> Response[MFADisableEmailCancelResponse | Problem]:
    """Cancel a pending email-assisted MFA disable.

     Cancels every pending email-assisted MFA disable on the account,
    so its emailed link stops working. A browser session must have
    passed the second factor; a session from a password reset alone
    is refused. Signing in with the authenticator app or a recovery
    code cancels a pending disable too. The owner is emailed when a
    request is cancelled.

    Args:
        body (MFADisableEmailCancelRequest | Unset): Optional body; a browser session sends the
            CSRF token minted for `mfa_disable_email_cancel`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MFADisableEmailCancelResponse | Problem]
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
    body: MFADisableEmailCancelRequest | Unset = UNSET,
) -> MFADisableEmailCancelResponse | Problem | None:
    """Cancel a pending email-assisted MFA disable.

     Cancels every pending email-assisted MFA disable on the account,
    so its emailed link stops working. A browser session must have
    passed the second factor; a session from a password reset alone
    is refused. Signing in with the authenticator app or a recovery
    code cancels a pending disable too. The owner is emailed when a
    request is cancelled.

    Args:
        body (MFADisableEmailCancelRequest | Unset): Optional body; a browser session sends the
            CSRF token minted for `mfa_disable_email_cancel`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MFADisableEmailCancelResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MFADisableEmailCancelRequest | Unset = UNSET,
) -> Response[MFADisableEmailCancelResponse | Problem]:
    """Cancel a pending email-assisted MFA disable.

     Cancels every pending email-assisted MFA disable on the account,
    so its emailed link stops working. A browser session must have
    passed the second factor; a session from a password reset alone
    is refused. Signing in with the authenticator app or a recovery
    code cancels a pending disable too. The owner is emailed when a
    request is cancelled.

    Args:
        body (MFADisableEmailCancelRequest | Unset): Optional body; a browser session sends the
            CSRF token minted for `mfa_disable_email_cancel`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MFADisableEmailCancelResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: MFADisableEmailCancelRequest | Unset = UNSET,
) -> MFADisableEmailCancelResponse | Problem | None:
    """Cancel a pending email-assisted MFA disable.

     Cancels every pending email-assisted MFA disable on the account,
    so its emailed link stops working. A browser session must have
    passed the second factor; a session from a password reset alone
    is refused. Signing in with the authenticator app or a recovery
    code cancels a pending disable too. The owner is emailed when a
    request is cancelled.

    Args:
        body (MFADisableEmailCancelRequest | Unset): Optional body; a browser session sends the
            CSRF token minted for `mfa_disable_email_cancel`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MFADisableEmailCancelResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
