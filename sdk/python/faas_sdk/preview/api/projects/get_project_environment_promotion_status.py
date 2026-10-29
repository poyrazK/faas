from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_environment_promotion_status_response import ProjectEnvironmentPromotionStatusResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    promotion: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/promotions/{promotion}".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            promotion=quote(str(promotion), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectEnvironmentPromotionStatusResponse | None:
    if response.status_code == 200:
        response_200 = ProjectEnvironmentPromotionStatusResponse.from_dict(response.json())

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
) -> Response[Problem | ProjectEnvironmentPromotionStatusResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    promotion: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ProjectEnvironmentPromotionStatusResponse]:
    """Get durable project environment promotion status.

     Returns the operation status, post-cutover verification result, and non-secret per-workload
    checkpoints for a promotion.

    Args:
        slug (str):
        environment (str):
        promotion (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentPromotionStatusResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        promotion=promotion,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    promotion: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ProjectEnvironmentPromotionStatusResponse | None:
    """Get durable project environment promotion status.

     Returns the operation status, post-cutover verification result, and non-secret per-workload
    checkpoints for a promotion.

    Args:
        slug (str):
        environment (str):
        promotion (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentPromotionStatusResponse
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        promotion=promotion,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    promotion: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ProjectEnvironmentPromotionStatusResponse]:
    """Get durable project environment promotion status.

     Returns the operation status, post-cutover verification result, and non-secret per-workload
    checkpoints for a promotion.

    Args:
        slug (str):
        environment (str):
        promotion (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentPromotionStatusResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        promotion=promotion,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    promotion: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ProjectEnvironmentPromotionStatusResponse | None:
    """Get durable project environment promotion status.

     Returns the operation status, post-cutover verification result, and non-secret per-workload
    checkpoints for a promotion.

    Args:
        slug (str):
        environment (str):
        promotion (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentPromotionStatusResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            promotion=promotion,
            client=client,
        )
    ).parsed
