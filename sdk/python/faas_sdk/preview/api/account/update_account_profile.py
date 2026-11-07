from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_profile_update import AccountProfileUpdate
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: AccountProfileUpdate,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/account",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountProfileUpdate | Problem | None:
    if response.status_code == 200:
        response_200 = AccountProfileUpdate.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AccountProfileUpdate | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: AccountProfileUpdate,
) -> Response[AccountProfileUpdate | Problem]:
    """Set the account display name.

     Sets the optional display name shown to org members and in the
    dashboard. Leading and trailing spaces are removed; an empty string
    clears it. At most 80 characters, no control characters.

    Args:
        body (AccountProfileUpdate): Body for `PATCH /v1/account`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountProfileUpdate | Problem]
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
    body: AccountProfileUpdate,
) -> AccountProfileUpdate | Problem | None:
    """Set the account display name.

     Sets the optional display name shown to org members and in the
    dashboard. Leading and trailing spaces are removed; an empty string
    clears it. At most 80 characters, no control characters.

    Args:
        body (AccountProfileUpdate): Body for `PATCH /v1/account`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountProfileUpdate | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: AccountProfileUpdate,
) -> Response[AccountProfileUpdate | Problem]:
    """Set the account display name.

     Sets the optional display name shown to org members and in the
    dashboard. Leading and trailing spaces are removed; an empty string
    clears it. At most 80 characters, no control characters.

    Args:
        body (AccountProfileUpdate): Body for `PATCH /v1/account`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountProfileUpdate | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: AccountProfileUpdate,
) -> AccountProfileUpdate | Problem | None:
    """Set the account display name.

     Sets the optional display name shown to org members and in the
    dashboard. Leading and trailing spaces are removed; an empty string
    clears it. At most 80 characters, no control characters.

    Args:
        body (AccountProfileUpdate): Body for `PATCH /v1/account`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountProfileUpdate | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
