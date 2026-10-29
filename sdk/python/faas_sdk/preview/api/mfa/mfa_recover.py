from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mfa_recover_request import MFARecoverRequest
from ...models.mfa_recover_response import MFARecoverResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: MFARecoverRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/mfa/recover",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MFARecoverResponse | Problem | None:
    if response.status_code == 200:
        response_200 = MFARecoverResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[MFARecoverResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MFARecoverRequest,
) -> Response[MFARecoverResponse | Problem]:
    """Burn a recovery code to regain access.

     Use when the customer's TOTP device is lost. Removes
    one matching SHA-256 hash from the stored set; if the
    customer burns the last code, /recover still works but
    /disable via recovery_code no longer does.

    Args:
        body (MFARecoverRequest): Body for /recover — one of the 10 recovery codes the
            customer received on /enroll. The hash is removed from
            the stored set; subsequent /recover calls with the same
            code return 401.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MFARecoverResponse | Problem]
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
    body: MFARecoverRequest,
) -> MFARecoverResponse | Problem | None:
    """Burn a recovery code to regain access.

     Use when the customer's TOTP device is lost. Removes
    one matching SHA-256 hash from the stored set; if the
    customer burns the last code, /recover still works but
    /disable via recovery_code no longer does.

    Args:
        body (MFARecoverRequest): Body for /recover — one of the 10 recovery codes the
            customer received on /enroll. The hash is removed from
            the stored set; subsequent /recover calls with the same
            code return 401.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MFARecoverResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: MFARecoverRequest,
) -> Response[MFARecoverResponse | Problem]:
    """Burn a recovery code to regain access.

     Use when the customer's TOTP device is lost. Removes
    one matching SHA-256 hash from the stored set; if the
    customer burns the last code, /recover still works but
    /disable via recovery_code no longer does.

    Args:
        body (MFARecoverRequest): Body for /recover — one of the 10 recovery codes the
            customer received on /enroll. The hash is removed from
            the stored set; subsequent /recover calls with the same
            code return 401.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MFARecoverResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: MFARecoverRequest,
) -> MFARecoverResponse | Problem | None:
    """Burn a recovery code to regain access.

     Use when the customer's TOTP device is lost. Removes
    one matching SHA-256 hash from the stored set; if the
    customer burns the last code, /recover still works but
    /disable via recovery_code no longer does.

    Args:
        body (MFARecoverRequest): Body for /recover — one of the 10 recovery codes the
            customer received on /enroll. The hash is removed from
            the stored set; subsequent /recover calls with the same
            code return 401.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MFARecoverResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
