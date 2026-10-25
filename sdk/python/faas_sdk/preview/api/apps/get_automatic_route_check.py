from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.automatic_route_check import AutomaticRouteCheck
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/route-requirements/checks/{deployment}".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AutomaticRouteCheck | Problem | None:
    if response.status_code == 200:
        response_200 = AutomaticRouteCheck.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
) -> Response[AutomaticRouteCheck | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AutomaticRouteCheck | Problem]:
    """Read the latest automatic route check and freshness.

     Read queue state and the latest stored deployment verdict in a consistent snapshot. Freshness
    compares current intent revision/hash, capture hash/truncation and configuration hash. A historical
    satisfied result can be stale. Requires apps:read or admin, completed MFA and current captured
    endpoint discovery entitlement. Does not run a new check or gate deployment.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomaticRouteCheck | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> AutomaticRouteCheck | Problem | None:
    """Read the latest automatic route check and freshness.

     Read queue state and the latest stored deployment verdict in a consistent snapshot. Freshness
    compares current intent revision/hash, capture hash/truncation and configuration hash. A historical
    satisfied result can be stale. Requires apps:read or admin, completed MFA and current captured
    endpoint discovery entitlement. Does not run a new check or gate deployment.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomaticRouteCheck | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AutomaticRouteCheck | Problem]:
    """Read the latest automatic route check and freshness.

     Read queue state and the latest stored deployment verdict in a consistent snapshot. Freshness
    compares current intent revision/hash, capture hash/truncation and configuration hash. A historical
    satisfied result can be stale. Requires apps:read or admin, completed MFA and current captured
    endpoint discovery entitlement. Does not run a new check or gate deployment.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomaticRouteCheck | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> AutomaticRouteCheck | Problem | None:
    """Read the latest automatic route check and freshness.

     Read queue state and the latest stored deployment verdict in a consistent snapshot. Freshness
    compares current intent revision/hash, capture hash/truncation and configuration hash. A historical
    satisfied result can be stale. Requires apps:read or admin, completed MFA and current captured
    endpoint discovery entitlement. Does not run a new check or gate deployment.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomaticRouteCheck | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            client=client,
        )
    ).parsed
