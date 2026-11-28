from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.issue_ownership_rules import IssueOwnershipRules
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: IssueOwnershipRules,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/issue-ownership-rules".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> IssueOwnershipRules | Problem | None:
    if response.status_code == 200:
        response_200 = IssueOwnershipRules.from_dict(response.json())

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
) -> Response[IssueOwnershipRules | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: IssueOwnershipRules,
) -> Response[IssueOwnershipRules | Problem]:
    """Replace the app's automatic issue ownership rules.

     Replaces the complete ordered policy and applies prospectively to new issue groups. Existing
    assignments are unchanged, and later manual assignments remain authoritative. Each target must be
    the app owner or an active organization member.

    Args:
        slug (str):
        body (IssueOwnershipRules): Complete ordered app-level ownership policy. An empty rules
            list disables automatic assignment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IssueOwnershipRules | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: IssueOwnershipRules,
) -> IssueOwnershipRules | Problem | None:
    """Replace the app's automatic issue ownership rules.

     Replaces the complete ordered policy and applies prospectively to new issue groups. Existing
    assignments are unchanged, and later manual assignments remain authoritative. Each target must be
    the app owner or an active organization member.

    Args:
        slug (str):
        body (IssueOwnershipRules): Complete ordered app-level ownership policy. An empty rules
            list disables automatic assignment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IssueOwnershipRules | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: IssueOwnershipRules,
) -> Response[IssueOwnershipRules | Problem]:
    """Replace the app's automatic issue ownership rules.

     Replaces the complete ordered policy and applies prospectively to new issue groups. Existing
    assignments are unchanged, and later manual assignments remain authoritative. Each target must be
    the app owner or an active organization member.

    Args:
        slug (str):
        body (IssueOwnershipRules): Complete ordered app-level ownership policy. An empty rules
            list disables automatic assignment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IssueOwnershipRules | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: IssueOwnershipRules,
) -> IssueOwnershipRules | Problem | None:
    """Replace the app's automatic issue ownership rules.

     Replaces the complete ordered policy and applies prospectively to new issue groups. Existing
    assignments are unchanged, and later manual assignments remain authoritative. Each target must be
    the app owner or an active organization member.

    Args:
        slug (str):
        body (IssueOwnershipRules): Complete ordered app-level ownership policy. An empty rules
            list disables automatic assignment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IssueOwnershipRules | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
