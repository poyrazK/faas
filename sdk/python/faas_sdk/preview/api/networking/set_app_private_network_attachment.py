from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_private_network_attachment_request import AppPrivateNetworkAttachmentRequest
from ...models.app_private_network_attachment_response import AppPrivateNetworkAttachmentResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: AppPrivateNetworkAttachmentRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/network/private".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppPrivateNetworkAttachmentResponse | Problem | None:
    if response.status_code == 202:
        response_202 = AppPrivateNetworkAttachmentResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

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
    body: AppPrivateNetworkAttachmentRequest,
) -> Response[AppPrivateNetworkAttachmentResponse | Problem]:
    """Request a private-network attachment.

     Replaces the app's provider-neutral attachment intent. Pro and Scale
    plans may request up to their plan CIDR cap (16 and 64 respectively).
    Network ID and region use the lowercase provider-neutral identifier
    grammar. CIDRs must be non-default IPv4 RFC1918 ranges, must not
    overlap each other, and must not overlap Gregale's reserved ranges.
    The response is `202 Accepted` with `status=pending`; traffic remains
    blocked until a connector advances the row to `ready`.

    Args:
        slug (str):
        body (AppPrivateNetworkAttachmentRequest): PUT body for /v1/apps/{slug}/network/private.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppPrivateNetworkAttachmentResponse | Problem]
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
    body: AppPrivateNetworkAttachmentRequest,
) -> AppPrivateNetworkAttachmentResponse | Problem | None:
    """Request a private-network attachment.

     Replaces the app's provider-neutral attachment intent. Pro and Scale
    plans may request up to their plan CIDR cap (16 and 64 respectively).
    Network ID and region use the lowercase provider-neutral identifier
    grammar. CIDRs must be non-default IPv4 RFC1918 ranges, must not
    overlap each other, and must not overlap Gregale's reserved ranges.
    The response is `202 Accepted` with `status=pending`; traffic remains
    blocked until a connector advances the row to `ready`.

    Args:
        slug (str):
        body (AppPrivateNetworkAttachmentRequest): PUT body for /v1/apps/{slug}/network/private.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppPrivateNetworkAttachmentResponse | Problem
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
    body: AppPrivateNetworkAttachmentRequest,
) -> Response[AppPrivateNetworkAttachmentResponse | Problem]:
    """Request a private-network attachment.

     Replaces the app's provider-neutral attachment intent. Pro and Scale
    plans may request up to their plan CIDR cap (16 and 64 respectively).
    Network ID and region use the lowercase provider-neutral identifier
    grammar. CIDRs must be non-default IPv4 RFC1918 ranges, must not
    overlap each other, and must not overlap Gregale's reserved ranges.
    The response is `202 Accepted` with `status=pending`; traffic remains
    blocked until a connector advances the row to `ready`.

    Args:
        slug (str):
        body (AppPrivateNetworkAttachmentRequest): PUT body for /v1/apps/{slug}/network/private.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppPrivateNetworkAttachmentResponse | Problem]
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
    body: AppPrivateNetworkAttachmentRequest,
) -> AppPrivateNetworkAttachmentResponse | Problem | None:
    """Request a private-network attachment.

     Replaces the app's provider-neutral attachment intent. Pro and Scale
    plans may request up to their plan CIDR cap (16 and 64 respectively).
    Network ID and region use the lowercase provider-neutral identifier
    grammar. CIDRs must be non-default IPv4 RFC1918 ranges, must not
    overlap each other, and must not overlap Gregale's reserved ranges.
    The response is `202 Accepted` with `status=pending`; traffic remains
    blocked until a connector advances the row to `ready`.

    Args:
        slug (str):
        body (AppPrivateNetworkAttachmentRequest): PUT body for /v1/apps/{slug}/network/private.

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
            body=body,
        )
    ).parsed
