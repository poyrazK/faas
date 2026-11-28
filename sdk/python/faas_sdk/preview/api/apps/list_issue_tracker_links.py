from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_issue_tracker_links_response import ListIssueTrackerLinksResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    issue_id: UUID,
    *,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/issues/{issue_id}/tracker-links".format(
            slug=quote(str(slug), safe=""),
            issue_id=quote(str(issue_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListIssueTrackerLinksResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListIssueTrackerLinksResponse.from_dict(response.json())

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
) -> Response[ListIssueTrackerLinksResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    issue_id: UUID,
    *,
    client: AuthenticatedClient,
    cursor: str | Unset = UNSET,
) -> Response[ListIssueTrackerLinksResponse | Problem]:
    """List GitHub and Linear references across this canonical issue group.

     Links remain attached to original issue identities through merges and splits. Returns at most 50
    references per page; cursors are bound to the app and canonical issue.

    Args:
        slug (str):
        issue_id (UUID):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListIssueTrackerLinksResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        issue_id=issue_id,
        cursor=cursor,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    issue_id: UUID,
    *,
    client: AuthenticatedClient,
    cursor: str | Unset = UNSET,
) -> ListIssueTrackerLinksResponse | Problem | None:
    """List GitHub and Linear references across this canonical issue group.

     Links remain attached to original issue identities through merges and splits. Returns at most 50
    references per page; cursors are bound to the app and canonical issue.

    Args:
        slug (str):
        issue_id (UUID):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListIssueTrackerLinksResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        issue_id=issue_id,
        client=client,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    slug: str,
    issue_id: UUID,
    *,
    client: AuthenticatedClient,
    cursor: str | Unset = UNSET,
) -> Response[ListIssueTrackerLinksResponse | Problem]:
    """List GitHub and Linear references across this canonical issue group.

     Links remain attached to original issue identities through merges and splits. Returns at most 50
    references per page; cursors are bound to the app and canonical issue.

    Args:
        slug (str):
        issue_id (UUID):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListIssueTrackerLinksResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        issue_id=issue_id,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    issue_id: UUID,
    *,
    client: AuthenticatedClient,
    cursor: str | Unset = UNSET,
) -> ListIssueTrackerLinksResponse | Problem | None:
    """List GitHub and Linear references across this canonical issue group.

     Links remain attached to original issue identities through merges and splits. Returns at most 50
    references per page; cursors are bound to the app and canonical issue.

    Args:
        slug (str):
        issue_id (UUID):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListIssueTrackerLinksResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            issue_id=issue_id,
            client=client,
            cursor=cursor,
        )
    ).parsed
