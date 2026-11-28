from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.latest_deployments_by_app_response import LatestDeploymentsByAppResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deployments/latest-by-app",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> LatestDeploymentsByAppResponse | Problem | None:
    if response.status_code == 200:
        response_200 = LatestDeploymentsByAppResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[LatestDeploymentsByAppResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[LatestDeploymentsByAppResponse | Problem]:
    """List the latest deployment for each app on the account.

     Returns at most one deployment for every non-deleted app owned by the
    authenticated account. Items are ordered newest first by `created_at`,
    with deployment ID as the stable tie-breaker.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[LatestDeploymentsByAppResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> LatestDeploymentsByAppResponse | Problem | None:
    """List the latest deployment for each app on the account.

     Returns at most one deployment for every non-deleted app owned by the
    authenticated account. Items are ordered newest first by `created_at`,
    with deployment ID as the stable tie-breaker.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        LatestDeploymentsByAppResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[LatestDeploymentsByAppResponse | Problem]:
    """List the latest deployment for each app on the account.

     Returns at most one deployment for every non-deleted app owned by the
    authenticated account. Items are ordered newest first by `created_at`,
    with deployment ID as the stable tie-breaker.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[LatestDeploymentsByAppResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> LatestDeploymentsByAppResponse | Problem | None:
    """List the latest deployment for each app on the account.

     Returns at most one deployment for every non-deleted app owned by the
    authenticated account. Items are ordered newest first by `created_at`,
    with deployment ID as the stable tie-breaker.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        LatestDeploymentsByAppResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
