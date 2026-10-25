from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_trusted_signer_list_response import AppTrustedSignerListResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/trusted_signers".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppTrustedSignerListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppTrustedSignerListResponse.from_dict(response.json())

        return response_200

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
) -> Response[AppTrustedSignerListResponse | Problem]:
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
) -> Response[AppTrustedSignerListResponse | Problem]:
    """List the per-app trusted-publisher list (admin).

     Lists every (signer_name, public_key_pem) row for this app
    (issue #472 / ADR-054). Admin-scoped; the wire form is the
    base64-encoded DER SPKI bytes, NOT a PEM-armoured block.
    Empty list is the EXPECTED state for any app with
    require_signed=false. Mounted with `authLimited → requireMFA →
    requireScope(ScopesAdminOnly...)`.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppTrustedSignerListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> AppTrustedSignerListResponse | Problem | None:
    """List the per-app trusted-publisher list (admin).

     Lists every (signer_name, public_key_pem) row for this app
    (issue #472 / ADR-054). Admin-scoped; the wire form is the
    base64-encoded DER SPKI bytes, NOT a PEM-armoured block.
    Empty list is the EXPECTED state for any app with
    require_signed=false. Mounted with `authLimited → requireMFA →
    requireScope(ScopesAdminOnly...)`.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppTrustedSignerListResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppTrustedSignerListResponse | Problem]:
    """List the per-app trusted-publisher list (admin).

     Lists every (signer_name, public_key_pem) row for this app
    (issue #472 / ADR-054). Admin-scoped; the wire form is the
    base64-encoded DER SPKI bytes, NOT a PEM-armoured block.
    Empty list is the EXPECTED state for any app with
    require_signed=false. Mounted with `authLimited → requireMFA →
    requireScope(ScopesAdminOnly...)`.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppTrustedSignerListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> AppTrustedSignerListResponse | Problem | None:
    """List the per-app trusted-publisher list (admin).

     Lists every (signer_name, public_key_pem) row for this app
    (issue #472 / ADR-054). Admin-scoped; the wire form is the
    base64-encoded DER SPKI bytes, NOT a PEM-armoured block.
    Empty list is the EXPECTED state for any app with
    require_signed=false. Mounted with `authLimited → requireMFA →
    requireScope(ScopesAdminOnly...)`.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppTrustedSignerListResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
