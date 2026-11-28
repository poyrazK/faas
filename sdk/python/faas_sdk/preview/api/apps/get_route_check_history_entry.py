from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_check_history_entry import RouteCheckHistoryEntry
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
    check_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/route-requirements/checks/{deployment}/history/{check_id}".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
            check_id=quote(str(check_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteCheckHistoryEntry | None:
    if response.status_code == 200:
        response_200 = RouteCheckHistoryEntry.from_dict(response.json())

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
) -> Response[Problem | RouteCheckHistoryEntry]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    check_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RouteCheckHistoryEntry]:
    """Read one retained immutable route check.

     Read the exact completion identified by a change notification. Missing or expired evidence returns
    404. Requires app read access, completed MFA and current captured endpoint discovery entitlement.
    Historical evidence does not establish current safety.

    Args:
        slug (str):
        deployment (UUID):
        check_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteCheckHistoryEntry]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        check_id=check_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    check_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RouteCheckHistoryEntry | None:
    """Read one retained immutable route check.

     Read the exact completion identified by a change notification. Missing or expired evidence returns
    404. Requires app read access, completed MFA and current captured endpoint discovery entitlement.
    Historical evidence does not establish current safety.

    Args:
        slug (str):
        deployment (UUID):
        check_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteCheckHistoryEntry
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        check_id=check_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    check_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RouteCheckHistoryEntry]:
    """Read one retained immutable route check.

     Read the exact completion identified by a change notification. Missing or expired evidence returns
    404. Requires app read access, completed MFA and current captured endpoint discovery entitlement.
    Historical evidence does not establish current safety.

    Args:
        slug (str):
        deployment (UUID):
        check_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteCheckHistoryEntry]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        check_id=check_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    check_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RouteCheckHistoryEntry | None:
    """Read one retained immutable route check.

     Read the exact completion identified by a change notification. Missing or expired evidence returns
    404. Requires app read access, completed MFA and current captured endpoint discovery entitlement.
    Historical evidence does not establish current safety.

    Args:
        slug (str):
        deployment (UUID):
        check_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteCheckHistoryEntry
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            check_id=check_id,
            client=client,
        )
    ).parsed
