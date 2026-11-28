from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.delete_account_session_body import DeleteAccountSessionBody
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    body: DeleteAccountSessionBody,
    faas_sid: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    cookies = {}
    if faas_sid is not UNSET:
        cookies["faas_sid"] = faas_sid

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/auth/sessions/{id}".format(
            id=quote(str(id), safe=""),
        ),
        "cookies": cookies,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

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
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DeleteAccountSessionBody,
    faas_sid: str | Unset = UNSET,
) -> Response[Any | Problem]:
    r"""Revoke a single session by id.

     Revokes the `sessions` row whose `id` is `{id}` and
    whose `account_id` matches the calling envelope. Cross-
    account DELETE returns 404 (not 403) — the handler
    never confirms a row exists in another account. Revoking
    the current session is allowed: the calling cookie is
    cleared on the wire (same as `/v1/auth/logout`).

    CSRF: action `session_revoke`.

    Emits `auth.session.revoke` with
    `reason: \"explicit\"`.

    Args:
        id (UUID):
        faas_sid (str | Unset):
        body (DeleteAccountSessionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        faas_sid=faas_sid,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DeleteAccountSessionBody,
    faas_sid: str | Unset = UNSET,
) -> Any | Problem | None:
    r"""Revoke a single session by id.

     Revokes the `sessions` row whose `id` is `{id}` and
    whose `account_id` matches the calling envelope. Cross-
    account DELETE returns 404 (not 403) — the handler
    never confirms a row exists in another account. Revoking
    the current session is allowed: the calling cookie is
    cleared on the wire (same as `/v1/auth/logout`).

    CSRF: action `session_revoke`.

    Emits `auth.session.revoke` with
    `reason: \"explicit\"`.

    Args:
        id (UUID):
        faas_sid (str | Unset):
        body (DeleteAccountSessionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        faas_sid=faas_sid,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DeleteAccountSessionBody,
    faas_sid: str | Unset = UNSET,
) -> Response[Any | Problem]:
    r"""Revoke a single session by id.

     Revokes the `sessions` row whose `id` is `{id}` and
    whose `account_id` matches the calling envelope. Cross-
    account DELETE returns 404 (not 403) — the handler
    never confirms a row exists in another account. Revoking
    the current session is allowed: the calling cookie is
    cleared on the wire (same as `/v1/auth/logout`).

    CSRF: action `session_revoke`.

    Emits `auth.session.revoke` with
    `reason: \"explicit\"`.

    Args:
        id (UUID):
        faas_sid (str | Unset):
        body (DeleteAccountSessionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        faas_sid=faas_sid,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DeleteAccountSessionBody,
    faas_sid: str | Unset = UNSET,
) -> Any | Problem | None:
    r"""Revoke a single session by id.

     Revokes the `sessions` row whose `id` is `{id}` and
    whose `account_id` matches the calling envelope. Cross-
    account DELETE returns 404 (not 403) — the handler
    never confirms a row exists in another account. Revoking
    the current session is allowed: the calling cookie is
    cleared on the wire (same as `/v1/auth/logout`).

    CSRF: action `session_revoke`.

    Emits `auth.session.revoke` with
    `reason: \"explicit\"`.

    Args:
        id (UUID):
        faas_sid (str | Unset):
        body (DeleteAccountSessionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            faas_sid=faas_sid,
        )
    ).parsed
