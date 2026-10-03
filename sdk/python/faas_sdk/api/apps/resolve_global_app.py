from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.global_app_resolution_response import GlobalAppResolutionResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    region: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/global-apps/{slug}/targets/{region}/resolve".format(
            slug=quote(str(slug), safe=""),
            region=quote(str(region), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GlobalAppResolutionResponse | Problem | None:
    if response.status_code == 200:
        response_200 = GlobalAppResolutionResponse.from_dict(response.json())

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
) -> Response[GlobalAppResolutionResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    region: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[GlobalAppResolutionResponse | Problem]:
    """Resolve a global identity in an explicit region.

     Returns the verified regional API origin and local app identity only while the cell is enabled and
    registry generation matches. Returns global_app_target_reverification_required after endpoint or
    policy changes. Does not forward the global key, select an implicit region, or establish
    health/readiness. Responses use Cache-Control no-store.

    Args:
        slug (str):
        region (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GlobalAppResolutionResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        region=region,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    region: str,
    *,
    client: AuthenticatedClient | Client,
) -> GlobalAppResolutionResponse | Problem | None:
    """Resolve a global identity in an explicit region.

     Returns the verified regional API origin and local app identity only while the cell is enabled and
    registry generation matches. Returns global_app_target_reverification_required after endpoint or
    policy changes. Does not forward the global key, select an implicit region, or establish
    health/readiness. Responses use Cache-Control no-store.

    Args:
        slug (str):
        region (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GlobalAppResolutionResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        region=region,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    region: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[GlobalAppResolutionResponse | Problem]:
    """Resolve a global identity in an explicit region.

     Returns the verified regional API origin and local app identity only while the cell is enabled and
    registry generation matches. Returns global_app_target_reverification_required after endpoint or
    policy changes. Does not forward the global key, select an implicit region, or establish
    health/readiness. Responses use Cache-Control no-store.

    Args:
        slug (str):
        region (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GlobalAppResolutionResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        region=region,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    region: str,
    *,
    client: AuthenticatedClient | Client,
) -> GlobalAppResolutionResponse | Problem | None:
    """Resolve a global identity in an explicit region.

     Returns the verified regional API origin and local app identity only while the cell is enabled and
    registry generation matches. Returns global_app_target_reverification_required after endpoint or
    policy changes. Does not forward the global key, select an implicit region, or establish
    health/readiness. Responses use Cache-Control no-store.

    Args:
        slug (str):
        region (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GlobalAppResolutionResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            region=region,
            client=client,
        )
    ).parsed
