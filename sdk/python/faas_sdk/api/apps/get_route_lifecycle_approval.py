from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_lifecycle_approval import RouteLifecycleApproval
from ...types import Response


def _get_kwargs(
    slug: str,
    approval_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/route-lifecycle/approvals/{approval_id}".format(
            slug=quote(str(slug), safe=""),
            approval_id=quote(str(approval_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteLifecycleApproval | None:
    if response.status_code == 200:
        response_200 = RouteLifecycleApproval.from_dict(response.json())

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
) -> Response[Problem | RouteLifecycleApproval]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    approval_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RouteLifecycleApproval]:
    """Inspect an app-owned lifecycle approval receipt.

     Requires read authorization and completed MFA. Returns the persisted review bindings and capture
    invalidation timestamp. An expired or policy-stale receipt remains readable and does not authorize a
    production traffic increase.

    Args:
        slug (str):
        approval_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteLifecycleApproval]
    """

    kwargs = _get_kwargs(
        slug=slug,
        approval_id=approval_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    approval_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RouteLifecycleApproval | None:
    """Inspect an app-owned lifecycle approval receipt.

     Requires read authorization and completed MFA. Returns the persisted review bindings and capture
    invalidation timestamp. An expired or policy-stale receipt remains readable and does not authorize a
    production traffic increase.

    Args:
        slug (str):
        approval_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteLifecycleApproval
    """

    return sync_detailed(
        slug=slug,
        approval_id=approval_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    approval_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RouteLifecycleApproval]:
    """Inspect an app-owned lifecycle approval receipt.

     Requires read authorization and completed MFA. Returns the persisted review bindings and capture
    invalidation timestamp. An expired or policy-stale receipt remains readable and does not authorize a
    production traffic increase.

    Args:
        slug (str):
        approval_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteLifecycleApproval]
    """

    kwargs = _get_kwargs(
        slug=slug,
        approval_id=approval_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    approval_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RouteLifecycleApproval | None:
    """Inspect an app-owned lifecycle approval receipt.

     Requires read authorization and completed MFA. Returns the persisted review bindings and capture
    invalidation timestamp. An expired or policy-stale receipt remains readable and does not authorize a
    production traffic increase.

    Args:
        slug (str):
        approval_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteLifecycleApproval
    """

    return (
        await asyncio_detailed(
            slug=slug,
            approval_id=approval_id,
            client=client,
        )
    ).parsed
