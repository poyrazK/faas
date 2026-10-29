from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_secret_revocation_response import AppSecretRevocationResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    revocation_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/secret-revocations/{revocation_id}".format(
            slug=quote(str(slug), safe=""),
            revocation_id=quote(str(revocation_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppSecretRevocationResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppSecretRevocationResponse.from_dict(response.json())

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
) -> Response[AppSecretRevocationResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    revocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppSecretRevocationResponse | Problem]:
    """Read runtime acknowledgements for a deleted secret.

     Returns the value-free runtime roster captured at deletion and its latest application
    acknowledgement state.

    Args:
        slug (str):
        revocation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppSecretRevocationResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        revocation_id=revocation_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    revocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> AppSecretRevocationResponse | Problem | None:
    """Read runtime acknowledgements for a deleted secret.

     Returns the value-free runtime roster captured at deletion and its latest application
    acknowledgement state.

    Args:
        slug (str):
        revocation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppSecretRevocationResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        revocation_id=revocation_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    revocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppSecretRevocationResponse | Problem]:
    """Read runtime acknowledgements for a deleted secret.

     Returns the value-free runtime roster captured at deletion and its latest application
    acknowledgement state.

    Args:
        slug (str):
        revocation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppSecretRevocationResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        revocation_id=revocation_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    revocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> AppSecretRevocationResponse | Problem | None:
    """Read runtime acknowledgements for a deleted secret.

     Returns the value-free runtime roster captured at deletion and its latest application
    acknowledgement state.

    Args:
        slug (str):
        revocation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppSecretRevocationResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            revocation_id=revocation_id,
            client=client,
        )
    ).parsed
