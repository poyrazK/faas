from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.rotate_deploy_token_request import RotateDeployTokenRequest
from ...models.rotate_deploy_token_response import RotateDeployTokenResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: RotateDeployTokenRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/deploy-tokens/{id}/rotate".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RotateDeployTokenResponse | None:
    if response.status_code == 201:
        response_201 = RotateDeployTokenResponse.from_dict(response.json())

        return response_201

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RotateDeployTokenResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RotateDeployTokenRequest | Unset = UNSET,
) -> Response[Problem | RotateDeployTokenResponse]:
    """Rotate an app deploy token.

    Args:
        slug (str):
        id (UUID):
        body (RotateDeployTokenRequest | Unset): Rotate a deploy token. Empty label inherits the
            predecessor; empty expiry defaults to 90 days.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RotateDeployTokenResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RotateDeployTokenRequest | Unset = UNSET,
) -> Problem | RotateDeployTokenResponse | None:
    """Rotate an app deploy token.

    Args:
        slug (str):
        id (UUID):
        body (RotateDeployTokenRequest | Unset): Rotate a deploy token. Empty label inherits the
            predecessor; empty expiry defaults to 90 days.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RotateDeployTokenResponse
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RotateDeployTokenRequest | Unset = UNSET,
) -> Response[Problem | RotateDeployTokenResponse]:
    """Rotate an app deploy token.

    Args:
        slug (str):
        id (UUID):
        body (RotateDeployTokenRequest | Unset): Rotate a deploy token. Empty label inherits the
            predecessor; empty expiry defaults to 90 days.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RotateDeployTokenResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RotateDeployTokenRequest | Unset = UNSET,
) -> Problem | RotateDeployTokenResponse | None:
    """Rotate an app deploy token.

    Args:
        slug (str):
        id (UUID):
        body (RotateDeployTokenRequest | Unset): Rotate a deploy token. Empty label inherits the
            predecessor; empty expiry defaults to 90 days.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RotateDeployTokenResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
