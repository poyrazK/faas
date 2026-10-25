from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mfa_enroll_response import MFAEnrollResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/mfa/enroll",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MFAEnrollResponse | Problem | None:
    if response.status_code == 200:
        response_200 = MFAEnrollResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[MFAEnrollResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[MFAEnrollResponse | Problem]:
    """Start MFA enrollment.

     Returns the TOTP secret, QR PNG, otpauth URL, and 10
    recovery codes exactly once. The plaintext secret is
    shown only here; the server stores a sealed copy. Call
    `/v1/account/mfa/confirm` with the customer's first
    6-digit code to commit the enrollment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MFAEnrollResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> MFAEnrollResponse | Problem | None:
    """Start MFA enrollment.

     Returns the TOTP secret, QR PNG, otpauth URL, and 10
    recovery codes exactly once. The plaintext secret is
    shown only here; the server stores a sealed copy. Call
    `/v1/account/mfa/confirm` with the customer's first
    6-digit code to commit the enrollment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MFAEnrollResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[MFAEnrollResponse | Problem]:
    """Start MFA enrollment.

     Returns the TOTP secret, QR PNG, otpauth URL, and 10
    recovery codes exactly once. The plaintext secret is
    shown only here; the server stores a sealed copy. Call
    `/v1/account/mfa/confirm` with the customer's first
    6-digit code to commit the enrollment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MFAEnrollResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> MFAEnrollResponse | Problem | None:
    """Start MFA enrollment.

     Returns the TOTP secret, QR PNG, otpauth URL, and 10
    recovery codes exactly once. The plaintext secret is
    shown only here; the server stores a sealed copy. Call
    `/v1/account/mfa/confirm` with the customer's first
    6-digit code to commit the enrollment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MFAEnrollResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
