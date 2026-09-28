from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_secret_response import AppSecretResponse
from ...models.problem import Problem
from ...models.put_app_secret_request import PutAppSecretRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    key: str,
    *,
    body: PutAppSecretRequest,
    scope: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["scope"] = scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/secrets/{key}".format(
            slug=quote(str(slug), safe=""),
            key=quote(str(key), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppSecretResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppSecretResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppSecretResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAppSecretRequest,
    scope: str | Unset = UNSET,
) -> Response[AppSecretResponse | Problem]:
    """Set a sealed secret.

     Seals the plaintext value against the host X25519 recipient and
    persists the ciphertext. The plaintext never lands in PG. Existing
    snapshots are invalidated; running processes retain their current
    environment until a cold wake or `POST /restart?fresh=true`.

    Args:
        slug (str):
        key (str):
        scope (str | Unset):
        body (PutAppSecretRequest): Set a secret: key name and plaintext (sealed at rest
            immediately, plaintext discarded after seal). secret_class is optional; omission preserves
            an existing class and defaults new rows to persistent. Ephemeral values prevent future
            init/warm captures for the app scope; older artifacts age out under normal snapshot
            garbage collection.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppSecretResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        key=key,
        body=body,
        scope=scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAppSecretRequest,
    scope: str | Unset = UNSET,
) -> AppSecretResponse | Problem | None:
    """Set a sealed secret.

     Seals the plaintext value against the host X25519 recipient and
    persists the ciphertext. The plaintext never lands in PG. Existing
    snapshots are invalidated; running processes retain their current
    environment until a cold wake or `POST /restart?fresh=true`.

    Args:
        slug (str):
        key (str):
        scope (str | Unset):
        body (PutAppSecretRequest): Set a secret: key name and plaintext (sealed at rest
            immediately, plaintext discarded after seal). secret_class is optional; omission preserves
            an existing class and defaults new rows to persistent. Ephemeral values prevent future
            init/warm captures for the app scope; older artifacts age out under normal snapshot
            garbage collection.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppSecretResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        key=key,
        client=client,
        body=body,
        scope=scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAppSecretRequest,
    scope: str | Unset = UNSET,
) -> Response[AppSecretResponse | Problem]:
    """Set a sealed secret.

     Seals the plaintext value against the host X25519 recipient and
    persists the ciphertext. The plaintext never lands in PG. Existing
    snapshots are invalidated; running processes retain their current
    environment until a cold wake or `POST /restart?fresh=true`.

    Args:
        slug (str):
        key (str):
        scope (str | Unset):
        body (PutAppSecretRequest): Set a secret: key name and plaintext (sealed at rest
            immediately, plaintext discarded after seal). secret_class is optional; omission preserves
            an existing class and defaults new rows to persistent. Ephemeral values prevent future
            init/warm captures for the app scope; older artifacts age out under normal snapshot
            garbage collection.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppSecretResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        key=key,
        body=body,
        scope=scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAppSecretRequest,
    scope: str | Unset = UNSET,
) -> AppSecretResponse | Problem | None:
    """Set a sealed secret.

     Seals the plaintext value against the host X25519 recipient and
    persists the ciphertext. The plaintext never lands in PG. Existing
    snapshots are invalidated; running processes retain their current
    environment until a cold wake or `POST /restart?fresh=true`.

    Args:
        slug (str):
        key (str):
        scope (str | Unset):
        body (PutAppSecretRequest): Set a secret: key name and plaintext (sealed at rest
            immediately, plaintext discarded after seal). secret_class is optional; omission preserves
            an existing class and defaults new rows to persistent. Ephemeral values prevent future
            init/warm captures for the app scope; older artifacts age out under normal snapshot
            garbage collection.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppSecretResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            key=key,
            client=client,
            body=body,
            scope=scope,
        )
    ).parsed
