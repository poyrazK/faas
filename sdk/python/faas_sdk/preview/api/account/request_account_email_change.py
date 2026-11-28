from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_email_change_request import AccountEmailChangeRequest
from ...models.problem import Problem
from ...models.request_account_email_change_response_202 import RequestAccountEmailChangeResponse202
from ...types import Response


def _get_kwargs(
    *,
    body: AccountEmailChangeRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/account/email",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RequestAccountEmailChangeResponse202 | None:
    if response.status_code == 202:
        response_202 = RequestAccountEmailChangeResponse202.from_dict(response.json())

        return response_202

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RequestAccountEmailChangeResponse202]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: AccountEmailChangeRequest,
) -> Response[Problem | RequestAccountEmailChangeResponse202]:
    """Start a sign-in email change.

     Browser sessions only; an API key gets 403. Accounts with two-factor
    authentication need a recent authenticator verification
    (`step_up_required` otherwise); accounts with a password send
    `current_password`. A single-use link valid for 1 hour is emailed to
    the new address; opening it and confirming moves the account and
    signs out every browser. The previous address gets a notice. A new
    address that already belongs to an account gets the same response
    and no email. CSRF: action `account_email_change`.

    Args:
        body (AccountEmailChangeRequest): Body for `POST /v1/account/email`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RequestAccountEmailChangeResponse202]
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
    body: AccountEmailChangeRequest,
) -> Problem | RequestAccountEmailChangeResponse202 | None:
    """Start a sign-in email change.

     Browser sessions only; an API key gets 403. Accounts with two-factor
    authentication need a recent authenticator verification
    (`step_up_required` otherwise); accounts with a password send
    `current_password`. A single-use link valid for 1 hour is emailed to
    the new address; opening it and confirming moves the account and
    signs out every browser. The previous address gets a notice. A new
    address that already belongs to an account gets the same response
    and no email. CSRF: action `account_email_change`.

    Args:
        body (AccountEmailChangeRequest): Body for `POST /v1/account/email`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RequestAccountEmailChangeResponse202
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: AccountEmailChangeRequest,
) -> Response[Problem | RequestAccountEmailChangeResponse202]:
    """Start a sign-in email change.

     Browser sessions only; an API key gets 403. Accounts with two-factor
    authentication need a recent authenticator verification
    (`step_up_required` otherwise); accounts with a password send
    `current_password`. A single-use link valid for 1 hour is emailed to
    the new address; opening it and confirming moves the account and
    signs out every browser. The previous address gets a notice. A new
    address that already belongs to an account gets the same response
    and no email. CSRF: action `account_email_change`.

    Args:
        body (AccountEmailChangeRequest): Body for `POST /v1/account/email`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RequestAccountEmailChangeResponse202]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: AccountEmailChangeRequest,
) -> Problem | RequestAccountEmailChangeResponse202 | None:
    """Start a sign-in email change.

     Browser sessions only; an API key gets 403. Accounts with two-factor
    authentication need a recent authenticator verification
    (`step_up_required` otherwise); accounts with a password send
    `current_password`. A single-use link valid for 1 hour is emailed to
    the new address; opening it and confirming moves the account and
    signs out every browser. The previous address gets a notice. A new
    address that already belongs to an account gets the same response
    and no email. CSRF: action `account_email_change`.

    Args:
        body (AccountEmailChangeRequest): Body for `POST /v1/account/email`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RequestAccountEmailChangeResponse202
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
