from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.unlink_account_sign_in_method_provider import (
    UnlinkAccountSignInMethodProvider,
)
from ...types import Response


def _get_kwargs(
    provider: UnlinkAccountSignInMethodProvider,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/account/sign-in-methods/{provider}".format(
            provider=quote(str(provider), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    provider: UnlinkAccountSignInMethodProvider,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Remove a linked Google or GitHub sign-in.

     Browser sessions need a recent authenticator verification when
    two-factor authentication is on. Refused with 409 when it is the
    last way to sign in; set a password or link another provider first.
    The provider stays removed: signing in with it no longer links it by
    matching email, until it is linked again from a browser signed in to
    the account. CSRF: action `sign_in_method_unlink` (browser sessions
    only; API keys need no token).

    Args:
        provider (UnlinkAccountSignInMethodProvider):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        provider=provider,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    provider: UnlinkAccountSignInMethodProvider,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Remove a linked Google or GitHub sign-in.

     Browser sessions need a recent authenticator verification when
    two-factor authentication is on. Refused with 409 when it is the
    last way to sign in; set a password or link another provider first.
    The provider stays removed: signing in with it no longer links it by
    matching email, until it is linked again from a browser signed in to
    the account. CSRF: action `sign_in_method_unlink` (browser sessions
    only; API keys need no token).

    Args:
        provider (UnlinkAccountSignInMethodProvider):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        provider=provider,
        client=client,
    ).parsed


async def asyncio_detailed(
    provider: UnlinkAccountSignInMethodProvider,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Remove a linked Google or GitHub sign-in.

     Browser sessions need a recent authenticator verification when
    two-factor authentication is on. Refused with 409 when it is the
    last way to sign in; set a password or link another provider first.
    The provider stays removed: signing in with it no longer links it by
    matching email, until it is linked again from a browser signed in to
    the account. CSRF: action `sign_in_method_unlink` (browser sessions
    only; API keys need no token).

    Args:
        provider (UnlinkAccountSignInMethodProvider):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        provider=provider,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    provider: UnlinkAccountSignInMethodProvider,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Remove a linked Google or GitHub sign-in.

     Browser sessions need a recent authenticator verification when
    two-factor authentication is on. Refused with 409 when it is the
    last way to sign in; set a password or link another provider first.
    The provider stays removed: signing in with it no longer links it by
    matching email, until it is linked again from a browser signed in to
    the account. CSRF: action `sign_in_method_unlink` (browser sessions
    only; API keys need no token).

    Args:
        provider (UnlinkAccountSignInMethodProvider):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            provider=provider,
            client=client,
        )
    ).parsed
