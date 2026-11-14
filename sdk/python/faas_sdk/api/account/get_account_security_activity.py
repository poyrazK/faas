from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.security_activity_response import SecurityActivityResponse
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/security-activity",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SecurityActivityResponse | None:
    if response.status_code == 200:
        response_200 = SecurityActivityResponse.from_dict(response.json())

        return response_200

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
) -> Response[Problem | SecurityActivityResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | SecurityActivityResponse]:
    """List the account's sign-ins and security changes.

     Sign-ins, API keys and CLI sign-ins, password, email, two-factor
    and sign-in method changes of the last 90 days, newest first, at
    most 100. Each event carries a fixed summary, and the client address
    and sign-in method when they were recorded. Needs a full-access
    credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SecurityActivityResponse]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> Problem | SecurityActivityResponse | None:
    """List the account's sign-ins and security changes.

     Sign-ins, API keys and CLI sign-ins, password, email, two-factor
    and sign-in method changes of the last 90 days, newest first, at
    most 100. Each event carries a fixed summary, and the client address
    and sign-in method when they were recorded. Needs a full-access
    credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SecurityActivityResponse
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | SecurityActivityResponse]:
    """List the account's sign-ins and security changes.

     Sign-ins, API keys and CLI sign-ins, password, email, two-factor
    and sign-in method changes of the last 90 days, newest first, at
    most 100. Each event carries a fixed summary, and the client address
    and sign-in method when they were recorded. Needs a full-access
    credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SecurityActivityResponse]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> Problem | SecurityActivityResponse | None:
    """List the account's sign-ins and security changes.

     Sign-ins, API keys and CLI sign-ins, password, email, two-factor
    and sign-in method changes of the last 90 days, newest first, at
    most 100. Each event carries a fixed summary, and the client address
    and sign-in method when they were recorded. Needs a full-access
    credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SecurityActivityResponse
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
