from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.refresh_automatic_route_check_response_202 import RefreshAutomaticRouteCheckResponse202
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/route-requirements/checks/{deployment}/refresh".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RefreshAutomaticRouteCheckResponse202 | None:
    if response.status_code == 202:
        response_202 = RefreshAutomaticRouteCheckResponse202.from_dict(response.json())

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
) -> Response[Problem | RefreshAutomaticRouteCheckResponse202]:
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
) -> Response[Problem | RefreshAutomaticRouteCheckResponse202]:
    """Queue a current route check for one deployment.

     Accept an empty body and durably queue current saved intent, capture and policy evaluation.
    Coalesces with pending work and resets a failed retry. Requires apps:read or admin, completed MFA
    and captured endpoint discovery entitlement. This action changes no gateway policy and makes no
    application requests. Poll getAutomaticRouteCheck for completion.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RefreshAutomaticRouteCheckResponse202]
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
) -> Problem | RefreshAutomaticRouteCheckResponse202 | None:
    """Queue a current route check for one deployment.

     Accept an empty body and durably queue current saved intent, capture and policy evaluation.
    Coalesces with pending work and resets a failed retry. Requires apps:read or admin, completed MFA
    and captured endpoint discovery entitlement. This action changes no gateway policy and makes no
    application requests. Poll getAutomaticRouteCheck for completion.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RefreshAutomaticRouteCheckResponse202
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
) -> Response[Problem | RefreshAutomaticRouteCheckResponse202]:
    """Queue a current route check for one deployment.

     Accept an empty body and durably queue current saved intent, capture and policy evaluation.
    Coalesces with pending work and resets a failed retry. Requires apps:read or admin, completed MFA
    and captured endpoint discovery entitlement. This action changes no gateway policy and makes no
    application requests. Poll getAutomaticRouteCheck for completion.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RefreshAutomaticRouteCheckResponse202]
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
) -> Problem | RefreshAutomaticRouteCheckResponse202 | None:
    """Queue a current route check for one deployment.

     Accept an empty body and durably queue current saved intent, capture and policy evaluation.
    Coalesces with pending work and resets a failed retry. Requires apps:read or admin, completed MFA
    and captured endpoint discovery entitlement. This action changes no gateway policy and makes no
    application requests. Poll getAutomaticRouteCheck for completion.

    Args:
        slug (str):
        deployment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RefreshAutomaticRouteCheckResponse202
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            client=client,
        )
    ).parsed
