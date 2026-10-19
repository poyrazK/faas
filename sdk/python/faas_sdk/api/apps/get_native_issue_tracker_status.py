from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.native_issue_tracker_status import NativeIssueTrackerStatus
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    issue_id: UUID,
    link_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/issues/{issue_id}/tracker-links/{link_id}/native-status".format(
            slug=quote(str(slug), safe=""),
            issue_id=quote(str(issue_id), safe=""),
            link_id=quote(str(link_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | NativeIssueTrackerStatus | Problem | None:
    if response.status_code == 200:
        response_200 = NativeIssueTrackerStatus.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 502:
        response_502 = cast(Any, None)
        return response_502

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | NativeIssueTrackerStatus | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    issue_id: UUID,
    link_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Any | NativeIssueTrackerStatus | Problem]:
    """Read a linked GitHub issue through the app owner's native installation.

     Requires Issues entitlement and app read access. Verifies the caller-owned installation, pinned
    bound repository identity, current repository visibility and issue-group reference membership.
    GitHub installation credentials stay server-side. Linear native connections are unsupported.

    Args:
        slug (str):
        issue_id (UUID):
        link_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | NativeIssueTrackerStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        issue_id=issue_id,
        link_id=link_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    issue_id: UUID,
    link_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Any | NativeIssueTrackerStatus | Problem | None:
    """Read a linked GitHub issue through the app owner's native installation.

     Requires Issues entitlement and app read access. Verifies the caller-owned installation, pinned
    bound repository identity, current repository visibility and issue-group reference membership.
    GitHub installation credentials stay server-side. Linear native connections are unsupported.

    Args:
        slug (str):
        issue_id (UUID):
        link_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | NativeIssueTrackerStatus | Problem
    """

    return sync_detailed(
        slug=slug,
        issue_id=issue_id,
        link_id=link_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    issue_id: UUID,
    link_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Any | NativeIssueTrackerStatus | Problem]:
    """Read a linked GitHub issue through the app owner's native installation.

     Requires Issues entitlement and app read access. Verifies the caller-owned installation, pinned
    bound repository identity, current repository visibility and issue-group reference membership.
    GitHub installation credentials stay server-side. Linear native connections are unsupported.

    Args:
        slug (str):
        issue_id (UUID):
        link_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | NativeIssueTrackerStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        issue_id=issue_id,
        link_id=link_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    issue_id: UUID,
    link_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Any | NativeIssueTrackerStatus | Problem | None:
    """Read a linked GitHub issue through the app owner's native installation.

     Requires Issues entitlement and app read access. Verifies the caller-owned installation, pinned
    bound repository identity, current repository visibility and issue-group reference membership.
    GitHub installation credentials stay server-side. Linear native connections are unsupported.

    Args:
        slug (str):
        issue_id (UUID):
        link_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | NativeIssueTrackerStatus | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            issue_id=issue_id,
            link_id=link_id,
            client=client,
        )
    ).parsed
