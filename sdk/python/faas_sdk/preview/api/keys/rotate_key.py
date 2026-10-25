from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.rotate_key_response import RotateKeyResponse
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/keys/{id}/rotate".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RotateKeyResponse | None:
    if response.status_code == 200:
        response_200 = RotateKeyResponse.from_dict(response.json())

        return response_200

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RotateKeyResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RotateKeyResponse]:
    """Rotate an API key.

     Mints a new key (status='active') and demotes the old key in
    a single transaction (issue #189 / IAM-5). The new key
    inherits the predecessor's label + scopes so the customer's
    CI config does not need to chase a label change.

    The old key's `expires_at` is OVERWRITTEN to the grace
    deadline (now + grace_window_days, where grace_window_days
    resolves from the per-account override or the 7-day plan
    default). Status flips to 'grace' for the window, then to
    'revoked' at the deadline. Setting
    `accounts.key_grace_window_days = 0` makes rotation atomic
    (old key revoked immediately).

    Returns the new plaintext exactly once — the old plaintext
    is not re-issued (we only store the SHA-256 hash). The
    customer's CI script captures the new plaintext at the
    moment of rotation and uses the new key thereafter; the old
    key remains valid only for the grace window.

    Quota: rotation is quota-neutral (-1 +1 = 0 net). A
    customer AT the per-account cap can still rotate.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RotateKeyResponse]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RotateKeyResponse | None:
    """Rotate an API key.

     Mints a new key (status='active') and demotes the old key in
    a single transaction (issue #189 / IAM-5). The new key
    inherits the predecessor's label + scopes so the customer's
    CI config does not need to chase a label change.

    The old key's `expires_at` is OVERWRITTEN to the grace
    deadline (now + grace_window_days, where grace_window_days
    resolves from the per-account override or the 7-day plan
    default). Status flips to 'grace' for the window, then to
    'revoked' at the deadline. Setting
    `accounts.key_grace_window_days = 0` makes rotation atomic
    (old key revoked immediately).

    Returns the new plaintext exactly once — the old plaintext
    is not re-issued (we only store the SHA-256 hash). The
    customer's CI script captures the new plaintext at the
    moment of rotation and uses the new key thereafter; the old
    key remains valid only for the grace window.

    Quota: rotation is quota-neutral (-1 +1 = 0 net). A
    customer AT the per-account cap can still rotate.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RotateKeyResponse
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RotateKeyResponse]:
    """Rotate an API key.

     Mints a new key (status='active') and demotes the old key in
    a single transaction (issue #189 / IAM-5). The new key
    inherits the predecessor's label + scopes so the customer's
    CI config does not need to chase a label change.

    The old key's `expires_at` is OVERWRITTEN to the grace
    deadline (now + grace_window_days, where grace_window_days
    resolves from the per-account override or the 7-day plan
    default). Status flips to 'grace' for the window, then to
    'revoked' at the deadline. Setting
    `accounts.key_grace_window_days = 0` makes rotation atomic
    (old key revoked immediately).

    Returns the new plaintext exactly once — the old plaintext
    is not re-issued (we only store the SHA-256 hash). The
    customer's CI script captures the new plaintext at the
    moment of rotation and uses the new key thereafter; the old
    key remains valid only for the grace window.

    Quota: rotation is quota-neutral (-1 +1 = 0 net). A
    customer AT the per-account cap can still rotate.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RotateKeyResponse]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RotateKeyResponse | None:
    """Rotate an API key.

     Mints a new key (status='active') and demotes the old key in
    a single transaction (issue #189 / IAM-5). The new key
    inherits the predecessor's label + scopes so the customer's
    CI config does not need to chase a label change.

    The old key's `expires_at` is OVERWRITTEN to the grace
    deadline (now + grace_window_days, where grace_window_days
    resolves from the per-account override or the 7-day plan
    default). Status flips to 'grace' for the window, then to
    'revoked' at the deadline. Setting
    `accounts.key_grace_window_days = 0` makes rotation atomic
    (old key revoked immediately).

    Returns the new plaintext exactly once — the old plaintext
    is not re-issued (we only store the SHA-256 hash). The
    customer's CI script captures the new plaintext at the
    moment of rotation and uses the new key thereafter; the old
    key remains valid only for the grace window.

    Quota: rotation is quota-neutral (-1 +1 = 0 net). A
    customer AT the per-account cap can still rotate.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RotateKeyResponse
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
