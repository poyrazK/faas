from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mfa_disable_email_request import MFADisableEmailRequest
from ...models.mfa_disable_email_response import MFADisableEmailResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: MFADisableEmailRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/mfa/disable-email",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MFADisableEmailResponse | Problem | None:
    if response.status_code == 200:
        response_200 = MFADisableEmailResponse.from_dict(response.json())

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
) -> Response[MFADisableEmailResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MFADisableEmailRequest,
) -> Response[MFADisableEmailResponse | Problem]:
    """Request email-assisted MFA disable.

     Sends a one-time confirmation link to the account email and
    starts a mandatory 24-hour server-side cooldown. A newer
    request invalidates any earlier link.

    Args:
        body (MFADisableEmailRequest): Body for requesting email-assisted MFA disable.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MFADisableEmailResponse | Problem]
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
    body: MFADisableEmailRequest,
) -> MFADisableEmailResponse | Problem | None:
    """Request email-assisted MFA disable.

     Sends a one-time confirmation link to the account email and
    starts a mandatory 24-hour server-side cooldown. A newer
    request invalidates any earlier link.

    Args:
        body (MFADisableEmailRequest): Body for requesting email-assisted MFA disable.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MFADisableEmailResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MFADisableEmailRequest,
) -> Response[MFADisableEmailResponse | Problem]:
    """Request email-assisted MFA disable.

     Sends a one-time confirmation link to the account email and
    starts a mandatory 24-hour server-side cooldown. A newer
    request invalidates any earlier link.

    Args:
        body (MFADisableEmailRequest): Body for requesting email-assisted MFA disable.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MFADisableEmailResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: MFADisableEmailRequest,
) -> MFADisableEmailResponse | Problem | None:
    """Request email-assisted MFA disable.

     Sends a one-time confirmation link to the account email and
    starts a mandatory 24-hour server-side cooldown. A newer
    request invalidates any earlier link.

    Args:
        body (MFADisableEmailRequest): Body for requesting email-assisted MFA disable.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MFADisableEmailResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
