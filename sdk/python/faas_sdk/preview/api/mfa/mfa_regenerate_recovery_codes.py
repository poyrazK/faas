from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mfa_regenerate_recovery_codes_body import MfaRegenerateRecoveryCodesBody
from ...models.problem import Problem
from ...models.recovery_codes_response import RecoveryCodesResponse
from ...types import Response


def _get_kwargs(
    *,
    body: MfaRegenerateRecoveryCodesBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/mfa/recovery-codes",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RecoveryCodesResponse | None:
    if response.status_code == 200:
        response_200 = RecoveryCodesResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RecoveryCodesResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MfaRegenerateRecoveryCodesBody,
) -> Response[Problem | RecoveryCodesResponse]:
    """Replace the recovery codes.

     Browser sessions with a recent authenticator verification only.
    Creates a new set of recovery codes, shown once; the previous codes
    stop working and the account gets an email notice. CSRF: action
    `mfa_recovery_codes`.

    Args:
        body (MfaRegenerateRecoveryCodesBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RecoveryCodesResponse]
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
    body: MfaRegenerateRecoveryCodesBody,
) -> Problem | RecoveryCodesResponse | None:
    """Replace the recovery codes.

     Browser sessions with a recent authenticator verification only.
    Creates a new set of recovery codes, shown once; the previous codes
    stop working and the account gets an email notice. CSRF: action
    `mfa_recovery_codes`.

    Args:
        body (MfaRegenerateRecoveryCodesBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RecoveryCodesResponse
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MfaRegenerateRecoveryCodesBody,
) -> Response[Problem | RecoveryCodesResponse]:
    """Replace the recovery codes.

     Browser sessions with a recent authenticator verification only.
    Creates a new set of recovery codes, shown once; the previous codes
    stop working and the account gets an email notice. CSRF: action
    `mfa_recovery_codes`.

    Args:
        body (MfaRegenerateRecoveryCodesBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RecoveryCodesResponse]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: MfaRegenerateRecoveryCodesBody,
) -> Problem | RecoveryCodesResponse | None:
    """Replace the recovery codes.

     Browser sessions with a recent authenticator verification only.
    Creates a new set of recovery codes, shown once; the previous codes
    stop working and the account gets an email notice. CSRF: action
    `mfa_recovery_codes`.

    Args:
        body (MfaRegenerateRecoveryCodesBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RecoveryCodesResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
