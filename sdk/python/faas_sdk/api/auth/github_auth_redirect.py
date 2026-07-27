from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/auth/github",
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 302:
        response_302 = cast(Any, None)
        return response_302

    if response.status_code == 500:
        response_500 = Problem.from_dict(response.json())

        return response_500

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
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """GitHub OAuth 2.0 consent redirect.

     Sets a 16-byte CSRF state cookie scoped to
    `/v1/auth/github/callback` and 302s to the GitHub consent
    with `scope=read:user user:email`. The callback requires
    a primary && verified email before minting a session
    (issue #165 PR #2, ADR-032).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """GitHub OAuth 2.0 consent redirect.

     Sets a 16-byte CSRF state cookie scoped to
    `/v1/auth/github/callback` and 302s to the GitHub consent
    with `scope=read:user user:email`. The callback requires
    a primary && verified email before minting a session
    (issue #165 PR #2, ADR-032).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """GitHub OAuth 2.0 consent redirect.

     Sets a 16-byte CSRF state cookie scoped to
    `/v1/auth/github/callback` and 302s to the GitHub consent
    with `scope=read:user user:email`. The callback requires
    a primary && verified email before minting a session
    (issue #165 PR #2, ADR-032).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """GitHub OAuth 2.0 consent redirect.

     Sets a 16-byte CSRF state cookie scoped to
    `/v1/auth/github/callback` and 302s to the GitHub consent
    with `scope=read:user user:email`. The callback requires
    a primary && verified email before minting a session
    (issue #165 PR #2, ADR-032).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
