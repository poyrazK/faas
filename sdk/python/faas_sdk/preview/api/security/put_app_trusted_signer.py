from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.add_trusted_signer_request import AddTrustedSignerRequest
from ...models.problem import Problem
from ...models.trusted_signer import TrustedSigner
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    *,
    body: AddTrustedSignerRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/trusted_signers/{name}".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | TrustedSigner | None:
    if response.status_code == 200:
        response_200 = TrustedSigner.from_dict(response.json())

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

    if response.status_code == 500:
        response_500 = Problem.from_dict(response.json())

        return response_500

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | TrustedSigner]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: AddTrustedSignerRequest,
) -> Response[Problem | TrustedSigner]:
    """Onboard (or replace) a trusted publisher (admin + MFA).

     Writes the (app_id, signer_name) row with the supplied base64-DER
    blob (issue #472 / ADR-054). apid decodes the blob and persists it
    verbatim; imaged mirrors the row to `/etc/faas/secrets/trusted-publishers/{name}.pem`
    and refreshes its in-memory trust cache on `pg_notify('trusted_signer_changed')`.
    PUT semantics: idempotent re-PUT replaces the previous blob.

    Audit event: `app.trusted_signer_added`.

    Args:
        slug (str):
        name (str):
        body (AddTrustedSignerRequest): PUT body for `/v1/apps/{slug}/trusted_signers/{name}`.
            `public_key_pem` is
            the base64-encoded DER blob (apid side strips PEM armour). The DER must
            parse as an ECDSA P-256 SPKI; any other curve or non-ECDSA key returns
            400 `trusted_signer_invalid`. Bytes length must land in [64, 1024].

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TrustedSigner]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: AddTrustedSignerRequest,
) -> Problem | TrustedSigner | None:
    """Onboard (or replace) a trusted publisher (admin + MFA).

     Writes the (app_id, signer_name) row with the supplied base64-DER
    blob (issue #472 / ADR-054). apid decodes the blob and persists it
    verbatim; imaged mirrors the row to `/etc/faas/secrets/trusted-publishers/{name}.pem`
    and refreshes its in-memory trust cache on `pg_notify('trusted_signer_changed')`.
    PUT semantics: idempotent re-PUT replaces the previous blob.

    Audit event: `app.trusted_signer_added`.

    Args:
        slug (str):
        name (str):
        body (AddTrustedSignerRequest): PUT body for `/v1/apps/{slug}/trusted_signers/{name}`.
            `public_key_pem` is
            the base64-encoded DER blob (apid side strips PEM armour). The DER must
            parse as an ECDSA P-256 SPKI; any other curve or non-ECDSA key returns
            400 `trusted_signer_invalid`. Bytes length must land in [64, 1024].

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TrustedSigner
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: AddTrustedSignerRequest,
) -> Response[Problem | TrustedSigner]:
    """Onboard (or replace) a trusted publisher (admin + MFA).

     Writes the (app_id, signer_name) row with the supplied base64-DER
    blob (issue #472 / ADR-054). apid decodes the blob and persists it
    verbatim; imaged mirrors the row to `/etc/faas/secrets/trusted-publishers/{name}.pem`
    and refreshes its in-memory trust cache on `pg_notify('trusted_signer_changed')`.
    PUT semantics: idempotent re-PUT replaces the previous blob.

    Audit event: `app.trusted_signer_added`.

    Args:
        slug (str):
        name (str):
        body (AddTrustedSignerRequest): PUT body for `/v1/apps/{slug}/trusted_signers/{name}`.
            `public_key_pem` is
            the base64-encoded DER blob (apid side strips PEM armour). The DER must
            parse as an ECDSA P-256 SPKI; any other curve or non-ECDSA key returns
            400 `trusted_signer_invalid`. Bytes length must land in [64, 1024].

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TrustedSigner]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: AddTrustedSignerRequest,
) -> Problem | TrustedSigner | None:
    """Onboard (or replace) a trusted publisher (admin + MFA).

     Writes the (app_id, signer_name) row with the supplied base64-DER
    blob (issue #472 / ADR-054). apid decodes the blob and persists it
    verbatim; imaged mirrors the row to `/etc/faas/secrets/trusted-publishers/{name}.pem`
    and refreshes its in-memory trust cache on `pg_notify('trusted_signer_changed')`.
    PUT semantics: idempotent re-PUT replaces the previous blob.

    Audit event: `app.trusted_signer_added`.

    Args:
        slug (str):
        name (str):
        body (AddTrustedSignerRequest): PUT body for `/v1/apps/{slug}/trusted_signers/{name}`.
            `public_key_pem` is
            the base64-encoded DER blob (apid side strips PEM armour). The DER must
            parse as an ECDSA P-256 SPKI; any other curve or non-ECDSA key returns
            400 `trusted_signer_invalid`. Bytes length must land in [64, 1024].

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TrustedSigner
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
        )
    ).parsed
