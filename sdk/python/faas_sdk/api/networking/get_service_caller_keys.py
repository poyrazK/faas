from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.service_caller_jwk_set import ServiceCallerJWKSet
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/service-caller-keys",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ServiceCallerJWKSet | None:
    if response.status_code == 200:
        response_200 = ServiceCallerJWKSet.from_dict(response.json())

        return response_200

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 500:
        response_500 = Problem.from_dict(response.json())

        return response_500

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ServiceCallerJWKSet]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ServiceCallerJWKSet]:
    """Read the public keys used to verify service-caller assertions.

     Unauthenticated, rate-limited JWKS containing only public Ed25519 keys.
    Keys retired during node rotation remain listed for the 30-second
    assertion lifetime. Cache responses only briefly and refresh on an
    unknown `kid` before rejecting an otherwise valid assertion.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ServiceCallerJWKSet]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ServiceCallerJWKSet | None:
    """Read the public keys used to verify service-caller assertions.

     Unauthenticated, rate-limited JWKS containing only public Ed25519 keys.
    Keys retired during node rotation remain listed for the 30-second
    assertion lifetime. Cache responses only briefly and refresh on an
    unknown `kid` before rejecting an otherwise valid assertion.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ServiceCallerJWKSet
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ServiceCallerJWKSet]:
    """Read the public keys used to verify service-caller assertions.

     Unauthenticated, rate-limited JWKS containing only public Ed25519 keys.
    Keys retired during node rotation remain listed for the 30-second
    assertion lifetime. Cache responses only briefly and refresh on an
    unknown `kid` before rejecting an otherwise valid assertion.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ServiceCallerJWKSet]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ServiceCallerJWKSet | None:
    """Read the public keys used to verify service-caller assertions.

     Unauthenticated, rate-limited JWKS containing only public Ed25519 keys.
    Keys retired during node rotation remain listed for the 30-second
    assertion lifetime. Cache responses only briefly and refresh on an
    unknown `kid` before rejecting an otherwise valid assertion.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ServiceCallerJWKSet
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
