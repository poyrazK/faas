from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mfa_disable_request import MFADisableRequest
from ...models.mfa_disable_response import MFADisableResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: MFADisableRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/mfa/disable",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MFADisableResponse | Problem | None:
    if response.status_code == 200:
        response_200 = MFADisableResponse.from_dict(response.json())

        return response_200

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[MFADisableResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MFADisableRequest,
) -> Response[MFADisableResponse | Problem]:
    """Opt out of MFA.

     Clears the TOTP secret, recovery codes, and
    mfa_enrolled_at. Body must include exactly one of
    `password` (re-verified against account_passwords)
    or `recovery_code` (consumed). Leaves mfa_required
    untouched so the plan-upgrade / 2nd-deploy
    chokepoints can re-arm.

    Args:
        body (MFADisableRequest): Body for /disable. Exactly one of `password` or
            `recovery_code` is required — both empty and both set are
            rejected with 400 CodeValidation. Password is verified
            against the existing `account_passwords.hash`; the
            recovery code is consumed (one-time).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MFADisableResponse | Problem]
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
    body: MFADisableRequest,
) -> MFADisableResponse | Problem | None:
    """Opt out of MFA.

     Clears the TOTP secret, recovery codes, and
    mfa_enrolled_at. Body must include exactly one of
    `password` (re-verified against account_passwords)
    or `recovery_code` (consumed). Leaves mfa_required
    untouched so the plan-upgrade / 2nd-deploy
    chokepoints can re-arm.

    Args:
        body (MFADisableRequest): Body for /disable. Exactly one of `password` or
            `recovery_code` is required — both empty and both set are
            rejected with 400 CodeValidation. Password is verified
            against the existing `account_passwords.hash`; the
            recovery code is consumed (one-time).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MFADisableResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MFADisableRequest,
) -> Response[MFADisableResponse | Problem]:
    """Opt out of MFA.

     Clears the TOTP secret, recovery codes, and
    mfa_enrolled_at. Body must include exactly one of
    `password` (re-verified against account_passwords)
    or `recovery_code` (consumed). Leaves mfa_required
    untouched so the plan-upgrade / 2nd-deploy
    chokepoints can re-arm.

    Args:
        body (MFADisableRequest): Body for /disable. Exactly one of `password` or
            `recovery_code` is required — both empty and both set are
            rejected with 400 CodeValidation. Password is verified
            against the existing `account_passwords.hash`; the
            recovery code is consumed (one-time).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MFADisableResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: MFADisableRequest,
) -> MFADisableResponse | Problem | None:
    """Opt out of MFA.

     Clears the TOTP secret, recovery codes, and
    mfa_enrolled_at. Body must include exactly one of
    `password` (re-verified against account_passwords)
    or `recovery_code` (consumed). Leaves mfa_required
    untouched so the plan-upgrade / 2nd-deploy
    chokepoints can re-arm.

    Args:
        body (MFADisableRequest): Body for /disable. Exactly one of `password` or
            `recovery_code` is required — both empty and both set are
            rejected with 400 CodeValidation. Password is verified
            against the existing `account_passwords.hash`; the
            recovery code is consumed (one-time).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MFADisableResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
