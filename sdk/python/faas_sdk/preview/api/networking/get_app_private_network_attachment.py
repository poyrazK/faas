from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_private_network_attachment_response import AppPrivateNetworkAttachmentResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/network/private".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppPrivateNetworkAttachmentResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppPrivateNetworkAttachmentResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppPrivateNetworkAttachmentResponse | Problem]:
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
) -> Response[AppPrivateNetworkAttachmentResponse | Problem]:
    """Read the app private-network attachment intent.

     Returns feature and plan capability metadata plus the current
    attachment, or `attachment=null` when none is configured. A pending
    or error attachment never admits private traffic.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppPrivateNetworkAttachmentResponse | Problem]
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
) -> AppPrivateNetworkAttachmentResponse | Problem | None:
    """Read the app private-network attachment intent.

     Returns feature and plan capability metadata plus the current
    attachment, or `attachment=null` when none is configured. A pending
    or error attachment never admits private traffic.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppPrivateNetworkAttachmentResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppPrivateNetworkAttachmentResponse | Problem]:
    """Read the app private-network attachment intent.

     Returns feature and plan capability metadata plus the current
    attachment, or `attachment=null` when none is configured. A pending
    or error attachment never admits private traffic.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppPrivateNetworkAttachmentResponse | Problem]
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
) -> AppPrivateNetworkAttachmentResponse | Problem | None:
    """Read the app private-network attachment intent.

     Returns feature and plan capability metadata plus the current
    attachment, or `attachment=null` when none is configured. A pending
    or error attachment never admits private traffic.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppPrivateNetworkAttachmentResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
