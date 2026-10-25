from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    faas_sid: str | Unset = UNSET,
) -> dict[str, Any]:

    cookies = {}
    if faas_sid is not UNSET:
        cookies["faas_sid"] = faas_sid

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/auth/logout",
        "cookies": cookies,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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
    faas_sid: str | Unset = UNSET,
) -> Response[Any | Problem]:
    r"""Log the current dashboard session out.

     Revokes the calling session row in the `sessions` table
    (one row per dashboard login; ADR-039 / IAM-3) and
    clears the `faas_sid` cookie. Always 204 on success,
    even if the row was already revoked (idempotent — the
    cookie is cleared either way). Other sessions on the
    same account are NOT touched; use
    `POST /v1/auth/sessions/revoke_all` for that.

    CSRF: action `logout` (verify via the
    `faas_csrf` cookie + body `csrf_token` field or
    `X-CSRF-Token` header).

    Emits `auth.session.revoke` with
    `reason: \"logout\"`.

    Args:
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        faas_sid=faas_sid,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    faas_sid: str | Unset = UNSET,
) -> Any | Problem | None:
    r"""Log the current dashboard session out.

     Revokes the calling session row in the `sessions` table
    (one row per dashboard login; ADR-039 / IAM-3) and
    clears the `faas_sid` cookie. Always 204 on success,
    even if the row was already revoked (idempotent — the
    cookie is cleared either way). Other sessions on the
    same account are NOT touched; use
    `POST /v1/auth/sessions/revoke_all` for that.

    CSRF: action `logout` (verify via the
    `faas_csrf` cookie + body `csrf_token` field or
    `X-CSRF-Token` header).

    Emits `auth.session.revoke` with
    `reason: \"logout\"`.

    Args:
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        client=client,
        faas_sid=faas_sid,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    faas_sid: str | Unset = UNSET,
) -> Response[Any | Problem]:
    r"""Log the current dashboard session out.

     Revokes the calling session row in the `sessions` table
    (one row per dashboard login; ADR-039 / IAM-3) and
    clears the `faas_sid` cookie. Always 204 on success,
    even if the row was already revoked (idempotent — the
    cookie is cleared either way). Other sessions on the
    same account are NOT touched; use
    `POST /v1/auth/sessions/revoke_all` for that.

    CSRF: action `logout` (verify via the
    `faas_csrf` cookie + body `csrf_token` field or
    `X-CSRF-Token` header).

    Emits `auth.session.revoke` with
    `reason: \"logout\"`.

    Args:
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        faas_sid=faas_sid,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    faas_sid: str | Unset = UNSET,
) -> Any | Problem | None:
    r"""Log the current dashboard session out.

     Revokes the calling session row in the `sessions` table
    (one row per dashboard login; ADR-039 / IAM-3) and
    clears the `faas_sid` cookie. Always 204 on success,
    even if the row was already revoked (idempotent — the
    cookie is cleared either way). Other sessions on the
    same account are NOT touched; use
    `POST /v1/auth/sessions/revoke_all` for that.

    CSRF: action `logout` (verify via the
    `faas_csrf` cookie + body `csrf_token` field or
    `X-CSRF-Token` header).

    Emits `auth.session.revoke` with
    `reason: \"logout\"`.

    Args:
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            faas_sid=faas_sid,
        )
    ).parsed
