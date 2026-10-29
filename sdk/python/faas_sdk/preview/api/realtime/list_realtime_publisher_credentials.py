from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.realtime_publisher_credential import RealtimePublisherCredential
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | list[RealtimePublisherCredential] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = RealtimePublisherCredential.from_dict(response_200_item_data)

            response_200.append(response_200_item)

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | list[RealtimePublisherCredential]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | list[RealtimePublisherCredential]]:
    """Read publisher grant and usage metadata

     Account/app-scoped publisher credential administration. Issuance/rotation/revocation require admin
    scope and MFA. Plaintext returned once; generic response caching is excluded. Revocation remains
    available when the endpoint or history preview is disabled.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[RealtimePublisherCredential]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient,
) -> Problem | list[RealtimePublisherCredential] | None:
    """Read publisher grant and usage metadata

     Account/app-scoped publisher credential administration. Issuance/rotation/revocation require admin
    scope and MFA. Plaintext returned once; generic response caching is excluded. Revocation remains
    available when the endpoint or history preview is disabled.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[RealtimePublisherCredential]
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | list[RealtimePublisherCredential]]:
    """Read publisher grant and usage metadata

     Account/app-scoped publisher credential administration. Issuance/rotation/revocation require admin
    scope and MFA. Plaintext returned once; generic response caching is excluded. Revocation remains
    available when the endpoint or history preview is disabled.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[RealtimePublisherCredential]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient,
) -> Problem | list[RealtimePublisherCredential] | None:
    """Read publisher grant and usage metadata

     Account/app-scoped publisher credential administration. Issuance/rotation/revocation require admin
    scope and MFA. Plaintext returned once; generic response caching is excluded. Revocation remains
    available when the endpoint or history preview is disabled.

    Args:
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[RealtimePublisherCredential]
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
        )
    ).parsed
