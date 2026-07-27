from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_key_response import APIKeyResponse
from ...models.create_key_request import CreateKeyRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CreateKeyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/keys",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIKeyResponse | Problem | None:
    if response.status_code == 201:
        response_201 = APIKeyResponse.from_dict(response.json())

        return response_201

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APIKeyResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateKeyRequest,
) -> Response[APIKeyResponse | Problem]:
    """Mint a new API key.

     Returns the plaintext key **once**. The plaintext is never stored
    and cannot be retrieved; subsequent GETs return only the prefix.

    Args:
        body (CreateKeyRequest): API key creation payload — label and optional scopes. Plaintext
            is returned exactly once in the 201 response. Scopes defaults to `["admin"]` when omitted
            so existing callers preserve the legacy full-access behavior. See IAM-1, ADR-034 rev2.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIKeyResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: CreateKeyRequest,
) -> APIKeyResponse | Problem | None:
    """Mint a new API key.

     Returns the plaintext key **once**. The plaintext is never stored
    and cannot be retrieved; subsequent GETs return only the prefix.

    Args:
        body (CreateKeyRequest): API key creation payload — label and optional scopes. Plaintext
            is returned exactly once in the 201 response. Scopes defaults to `["admin"]` when omitted
            so existing callers preserve the legacy full-access behavior. See IAM-1, ADR-034 rev2.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIKeyResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateKeyRequest,
) -> Response[APIKeyResponse | Problem]:
    """Mint a new API key.

     Returns the plaintext key **once**. The plaintext is never stored
    and cannot be retrieved; subsequent GETs return only the prefix.

    Args:
        body (CreateKeyRequest): API key creation payload — label and optional scopes. Plaintext
            is returned exactly once in the 201 response. Scopes defaults to `["admin"]` when omitted
            so existing callers preserve the legacy full-access behavior. See IAM-1, ADR-034 rev2.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIKeyResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreateKeyRequest,
) -> APIKeyResponse | Problem | None:
    """Mint a new API key.

     Returns the plaintext key **once**. The plaintext is never stored
    and cannot be retrieved; subsequent GETs return only the prefix.

    Args:
        body (CreateKeyRequest): API key creation payload — label and optional scopes. Plaintext
            is returned exactly once in the 201 response. Scopes defaults to `["admin"]` when omitted
            so existing callers preserve the legacy full-access behavior. See IAM-1, ADR-034 rev2.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIKeyResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
