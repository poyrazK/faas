from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_removal_check import RouteRemovalCheck
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    *,
    deployment_id: UUID,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_deployment_id = str(deployment_id)
    params["deployment_id"] = json_deployment_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/route-removal/check".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteRemovalCheck | None:
    if response.status_code == 200:
        response_200 = RouteRemovalCheck.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RouteRemovalCheck]:
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
    deployment_id: UUID,
) -> Response[Problem | RouteRemovalCheck]:
    """Read authoritative route removal blockers.

     Requires apps:read or admin and completed MFA. Uses the same evaluator as production traffic
    transitions. This read is advisory; enforcement repeats within the traffic transaction. Missing
    captures, stale approvals, changed policies or renewed old-route observations block enforce mode.

    Args:
        slug (str):
        deployment_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteRemovalCheck]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: UUID,
) -> Problem | RouteRemovalCheck | None:
    """Read authoritative route removal blockers.

     Requires apps:read or admin and completed MFA. Uses the same evaluator as production traffic
    transitions. This read is advisory; enforcement repeats within the traffic transaction. Missing
    captures, stale approvals, changed policies or renewed old-route observations block enforce mode.

    Args:
        slug (str):
        deployment_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteRemovalCheck
    """

    return sync_detailed(
        slug=slug,
        client=client,
        deployment_id=deployment_id,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: UUID,
) -> Response[Problem | RouteRemovalCheck]:
    """Read authoritative route removal blockers.

     Requires apps:read or admin and completed MFA. Uses the same evaluator as production traffic
    transitions. This read is advisory; enforcement repeats within the traffic transaction. Missing
    captures, stale approvals, changed policies or renewed old-route observations block enforce mode.

    Args:
        slug (str):
        deployment_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteRemovalCheck]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: UUID,
) -> Problem | RouteRemovalCheck | None:
    """Read authoritative route removal blockers.

     Requires apps:read or admin and completed MFA. Uses the same evaluator as production traffic
    transitions. This read is advisory; enforcement repeats within the traffic transaction. Missing
    captures, stale approvals, changed policies or renewed old-route observations block enforce mode.

    Args:
        slug (str):
        deployment_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteRemovalCheck
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            deployment_id=deployment_id,
        )
    ).parsed
