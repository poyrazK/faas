from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_registry_credential_response import AppRegistryCredentialResponse
from ...models.problem import Problem
from ...models.put_app_registry_credential_request import PutAppRegistryCredentialRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: PutAppRegistryCredentialRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/registry-credentials".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppRegistryCredentialResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppRegistryCredentialResponse.from_dict(response.json())

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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppRegistryCredentialResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAppRegistryCredentialRequest,
) -> Response[AppRegistryCredentialResponse | Problem]:
    r"""Set or replace a sealed private-registry credential.

     Seals the plaintext password against the host X25519 recipient
    (namespace `\"registry_creds\"`) and upserts the `(app_id, registry)`
    row. The plaintext never lands in PG. Re-PUTs of an existing
    `(app, host)` replace the ciphertext and bump `updated_at`
    WITHOUT consuming a new quota slot.

    Args:
        slug (str):
        body (PutAppRegistryCredentialRequest): Set a private-registry Basic Auth credential:
            normalized registry
            host + username + plaintext password. The password is sealed
            server-side under namespace `"registry_creds"` against the host
            X25519 recipient and never persisted in plaintext.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppRegistryCredentialResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAppRegistryCredentialRequest,
) -> AppRegistryCredentialResponse | Problem | None:
    r"""Set or replace a sealed private-registry credential.

     Seals the plaintext password against the host X25519 recipient
    (namespace `\"registry_creds\"`) and upserts the `(app_id, registry)`
    row. The plaintext never lands in PG. Re-PUTs of an existing
    `(app, host)` replace the ciphertext and bump `updated_at`
    WITHOUT consuming a new quota slot.

    Args:
        slug (str):
        body (PutAppRegistryCredentialRequest): Set a private-registry Basic Auth credential:
            normalized registry
            host + username + plaintext password. The password is sealed
            server-side under namespace `"registry_creds"` against the host
            X25519 recipient and never persisted in plaintext.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppRegistryCredentialResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAppRegistryCredentialRequest,
) -> Response[AppRegistryCredentialResponse | Problem]:
    r"""Set or replace a sealed private-registry credential.

     Seals the plaintext password against the host X25519 recipient
    (namespace `\"registry_creds\"`) and upserts the `(app_id, registry)`
    row. The plaintext never lands in PG. Re-PUTs of an existing
    `(app, host)` replace the ciphertext and bump `updated_at`
    WITHOUT consuming a new quota slot.

    Args:
        slug (str):
        body (PutAppRegistryCredentialRequest): Set a private-registry Basic Auth credential:
            normalized registry
            host + username + plaintext password. The password is sealed
            server-side under namespace `"registry_creds"` against the host
            X25519 recipient and never persisted in plaintext.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppRegistryCredentialResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAppRegistryCredentialRequest,
) -> AppRegistryCredentialResponse | Problem | None:
    r"""Set or replace a sealed private-registry credential.

     Seals the plaintext password against the host X25519 recipient
    (namespace `\"registry_creds\"`) and upserts the `(app_id, registry)`
    row. The plaintext never lands in PG. Re-PUTs of an existing
    `(app, host)` replace the ciphertext and bump `updated_at`
    WITHOUT consuming a new quota slot.

    Args:
        slug (str):
        body (PutAppRegistryCredentialRequest): Set a private-registry Basic Auth credential:
            normalized registry
            host + username + plaintext password. The password is sealed
            server-side under namespace `"registry_creds"` against the host
            X25519 recipient and never persisted in plaintext.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppRegistryCredentialResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
