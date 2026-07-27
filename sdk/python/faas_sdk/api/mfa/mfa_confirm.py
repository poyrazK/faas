from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mfa_confirm_request import MFAConfirmRequest
from ...models.mfa_confirm_response import MFAConfirmResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: MFAConfirmRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/mfa/confirm",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MFAConfirmResponse | Problem | None:
    if response.status_code == 200:
        response_200 = MFAConfirmResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | MFAConfirmResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MFAConfirmRequest,
) -> Response[Any | MFAConfirmResponse | Problem]:
    """Finish MFA enrollment with the first TOTP code.

     Verifies the 6-digit code against the sealed secret
    and stamps mfa_enrolled_at. Re-issues the session
    cookie without the mfa_pending flag.

    Args:
        body (MFAConfirmRequest): Body for /confirm — a single 6-digit TOTP code.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MFAConfirmResponse | Problem]
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
    body: MFAConfirmRequest,
) -> Any | MFAConfirmResponse | Problem | None:
    """Finish MFA enrollment with the first TOTP code.

     Verifies the 6-digit code against the sealed secret
    and stamps mfa_enrolled_at. Re-issues the session
    cookie without the mfa_pending flag.

    Args:
        body (MFAConfirmRequest): Body for /confirm — a single 6-digit TOTP code.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MFAConfirmResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MFAConfirmRequest,
) -> Response[Any | MFAConfirmResponse | Problem]:
    """Finish MFA enrollment with the first TOTP code.

     Verifies the 6-digit code against the sealed secret
    and stamps mfa_enrolled_at. Re-issues the session
    cookie without the mfa_pending flag.

    Args:
        body (MFAConfirmRequest): Body for /confirm — a single 6-digit TOTP code.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MFAConfirmResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: MFAConfirmRequest,
) -> Any | MFAConfirmResponse | Problem | None:
    """Finish MFA enrollment with the first TOTP code.

     Verifies the 6-digit code against the sealed secret
    and stamps mfa_enrolled_at. Re-issues the session
    cookie without the mfa_pending flag.

    Args:
        body (MFAConfirmRequest): Body for /confirm — a single 6-digit TOTP code.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MFAConfirmResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
