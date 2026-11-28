from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.native_linear_issue_tracker_status import NativeLinearIssueTrackerStatus
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    issue_id: UUID,
    link_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/issues/{issue_id}/tracker-links/{link_id}/native-linear-status".format(
            slug=quote(str(slug), safe=""),
            issue_id=quote(str(issue_id), safe=""),
            link_id=quote(str(link_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | NativeLinearIssueTrackerStatus | Problem | None:
    if response.status_code == 200:
        response_200 = NativeLinearIssueTrackerStatus.from_dict(response.json())

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
) -> Response[Any | NativeLinearIssueTrackerStatus | Problem]:
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
) -> Response[Any | NativeLinearIssueTrackerStatus | Problem]:
    """Preview a linked Linear lifecycle using the stored owner connection.

     Requires app read access and the app owner with Issues entitlement and deploy eligibility. Verifies
    the stored workspace/viewer/team and linked ticket identity. Returns status plus connection revision
    and group fingerprint for an explicit push. Archived and canceled or unfamiliar provider states
    require manual triage. No credentials are returned.

    Args:
        slug (str):
        issue_id (UUID):
        link_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | NativeLinearIssueTrackerStatus | Problem]
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
) -> Any | NativeLinearIssueTrackerStatus | Problem | None:
    """Preview a linked Linear lifecycle using the stored owner connection.

     Requires app read access and the app owner with Issues entitlement and deploy eligibility. Verifies
    the stored workspace/viewer/team and linked ticket identity. Returns status plus connection revision
    and group fingerprint for an explicit push. Archived and canceled or unfamiliar provider states
    require manual triage. No credentials are returned.

    Args:
        slug (str):
        issue_id (UUID):
        link_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | NativeLinearIssueTrackerStatus | Problem
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
) -> Response[Any | NativeLinearIssueTrackerStatus | Problem]:
    """Preview a linked Linear lifecycle using the stored owner connection.

     Requires app read access and the app owner with Issues entitlement and deploy eligibility. Verifies
    the stored workspace/viewer/team and linked ticket identity. Returns status plus connection revision
    and group fingerprint for an explicit push. Archived and canceled or unfamiliar provider states
    require manual triage. No credentials are returned.

    Args:
        slug (str):
        issue_id (UUID):
        link_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | NativeLinearIssueTrackerStatus | Problem]
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
) -> Any | NativeLinearIssueTrackerStatus | Problem | None:
    """Preview a linked Linear lifecycle using the stored owner connection.

     Requires app read access and the app owner with Issues entitlement and deploy eligibility. Verifies
    the stored workspace/viewer/team and linked ticket identity. Returns status plus connection revision
    and group fingerprint for an explicit push. Archived and canceled or unfamiliar provider states
    require manual triage. No credentials are returned.

    Args:
        slug (str):
        issue_id (UUID):
        link_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | NativeLinearIssueTrackerStatus | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            issue_id=issue_id,
            link_id=link_id,
            client=client,
        )
    ).parsed
