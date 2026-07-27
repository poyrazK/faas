from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    *,
    code: str,
    state: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["code"] = code

    params["state"] = state

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/auth/github/callback",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 302:
        response_302 = cast(Any, None)
        return response_302

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 500:
        response_500 = Problem.from_dict(response.json())

        return response_500

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

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
    code: str,
    state: str,
) -> Response[Any | Problem]:
    """GitHub OAuth 2.0 callback.

     Verifies state, exchanges the code, fetches `/user` and
    `/user/emails`, filters the primary && verified email,
    and signs the user in. Sub-first lookup against
    `oauth_links` enforces the §11 anti-takeover invariant.

    On success, `auth.login` is appended to the events table
    (ADR-035) with `method=github`, `email=<verified email>`,
    and `login=<GitHub username>` so the audit dashboard and
    the corroborating slog line reference one identifier.

    Args:
        code (str):
        state (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        code=code,
        state=state,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    code: str,
    state: str,
) -> Any | Problem | None:
    """GitHub OAuth 2.0 callback.

     Verifies state, exchanges the code, fetches `/user` and
    `/user/emails`, filters the primary && verified email,
    and signs the user in. Sub-first lookup against
    `oauth_links` enforces the §11 anti-takeover invariant.

    On success, `auth.login` is appended to the events table
    (ADR-035) with `method=github`, `email=<verified email>`,
    and `login=<GitHub username>` so the audit dashboard and
    the corroborating slog line reference one identifier.

    Args:
        code (str):
        state (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        client=client,
        code=code,
        state=state,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    code: str,
    state: str,
) -> Response[Any | Problem]:
    """GitHub OAuth 2.0 callback.

     Verifies state, exchanges the code, fetches `/user` and
    `/user/emails`, filters the primary && verified email,
    and signs the user in. Sub-first lookup against
    `oauth_links` enforces the §11 anti-takeover invariant.

    On success, `auth.login` is appended to the events table
    (ADR-035) with `method=github`, `email=<verified email>`,
    and `login=<GitHub username>` so the audit dashboard and
    the corroborating slog line reference one identifier.

    Args:
        code (str):
        state (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        code=code,
        state=state,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    code: str,
    state: str,
) -> Any | Problem | None:
    """GitHub OAuth 2.0 callback.

     Verifies state, exchanges the code, fetches `/user` and
    `/user/emails`, filters the primary && verified email,
    and signs the user in. Sub-first lookup against
    `oauth_links` enforces the §11 anti-takeover invariant.

    On success, `auth.login` is appended to the events table
    (ADR-035) with `method=github`, `email=<verified email>`,
    and `login=<GitHub username>` so the audit dashboard and
    the corroborating slog line reference one identifier.

    Args:
        code (str):
        state (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            code=code,
            state=state,
        )
    ).parsed
