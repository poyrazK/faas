from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.rotate_app_secret_request import RotateAppSecretRequest
from ...models.rotate_app_secret_response import RotateAppSecretResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    key: str,
    *,
    body: RotateAppSecretRequest,
    scope: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["scope"] = scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/secrets/{key}/rotate".format(
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
) -> Problem | RotateAppSecretResponse | None:
    if response.status_code == 200:
        response_200 = RotateAppSecretResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[Problem | RotateAppSecretResponse]:
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
    body: RotateAppSecretRequest,
    scope: str | Unset = UNSET,
) -> Response[Problem | RotateAppSecretResponse]:
    """Re-seal a sealed secret under the current host identity.

     Re-seals the `(app_id, key)` row under the current host X25519
    recipient and stamps the kid column. Emits `secret.rotated`
    audit kind when the row already had a value; emits `secret.set`
    when the row was previously empty (first-time rotation). The
    same byte cap as PUT applies (`SecretValueMaxBytes`). Existing
    snapshots are invalidated after the write.

    Args:
        slug (str):
        key (str):
        scope (str | Unset):
        body (RotateAppSecretRequest): Rotate a secret: new plaintext value (ADR-089 PR-B). Same
            wire
            shape as `PutAppSecretRequest`; the rotate verb is distinct so
            the server can emit the `secret.rotated` audit kind (vs
            `secret.set` on PUT). Byte cap is the per-plan
            `SecretValueMaxBytes`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RotateAppSecretResponse]
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
    body: RotateAppSecretRequest,
    scope: str | Unset = UNSET,
) -> Problem | RotateAppSecretResponse | None:
    """Re-seal a sealed secret under the current host identity.

     Re-seals the `(app_id, key)` row under the current host X25519
    recipient and stamps the kid column. Emits `secret.rotated`
    audit kind when the row already had a value; emits `secret.set`
    when the row was previously empty (first-time rotation). The
    same byte cap as PUT applies (`SecretValueMaxBytes`). Existing
    snapshots are invalidated after the write.

    Args:
        slug (str):
        key (str):
        scope (str | Unset):
        body (RotateAppSecretRequest): Rotate a secret: new plaintext value (ADR-089 PR-B). Same
            wire
            shape as `PutAppSecretRequest`; the rotate verb is distinct so
            the server can emit the `secret.rotated` audit kind (vs
            `secret.set` on PUT). Byte cap is the per-plan
            `SecretValueMaxBytes`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RotateAppSecretResponse
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
    body: RotateAppSecretRequest,
    scope: str | Unset = UNSET,
) -> Response[Problem | RotateAppSecretResponse]:
    """Re-seal a sealed secret under the current host identity.

     Re-seals the `(app_id, key)` row under the current host X25519
    recipient and stamps the kid column. Emits `secret.rotated`
    audit kind when the row already had a value; emits `secret.set`
    when the row was previously empty (first-time rotation). The
    same byte cap as PUT applies (`SecretValueMaxBytes`). Existing
    snapshots are invalidated after the write.

    Args:
        slug (str):
        key (str):
        scope (str | Unset):
        body (RotateAppSecretRequest): Rotate a secret: new plaintext value (ADR-089 PR-B). Same
            wire
            shape as `PutAppSecretRequest`; the rotate verb is distinct so
            the server can emit the `secret.rotated` audit kind (vs
            `secret.set` on PUT). Byte cap is the per-plan
            `SecretValueMaxBytes`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RotateAppSecretResponse]
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
    body: RotateAppSecretRequest,
    scope: str | Unset = UNSET,
) -> Problem | RotateAppSecretResponse | None:
    """Re-seal a sealed secret under the current host identity.

     Re-seals the `(app_id, key)` row under the current host X25519
    recipient and stamps the kid column. Emits `secret.rotated`
    audit kind when the row already had a value; emits `secret.set`
    when the row was previously empty (first-time rotation). The
    same byte cap as PUT applies (`SecretValueMaxBytes`). Existing
    snapshots are invalidated after the write.

    Args:
        slug (str):
        key (str):
        scope (str | Unset):
        body (RotateAppSecretRequest): Rotate a secret: new plaintext value (ADR-089 PR-B). Same
            wire
            shape as `PutAppSecretRequest`; the rotate verb is distinct so
            the server can emit the `secret.rotated` audit kind (vs
            `secret.set` on PUT). Byte cap is the per-plan
            `SecretValueMaxBytes`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RotateAppSecretResponse
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
