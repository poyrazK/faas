from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.advance_stored_workflow_live_suite_request import AdvanceStoredWorkflowLiveSuiteRequest
from ...models.problem import Problem
from ...models.stored_workflow_live_suite import StoredWorkflowLiveSuite
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    suite_id: UUID,
    *,
    body: AdvanceStoredWorkflowLiveSuiteRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/workflows/stored/{name}/live-suites/{suite_id}/advance".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
            suite_id=quote(str(suite_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | StoredWorkflowLiveSuite | None:
    if response.status_code == 200:
        response_200 = StoredWorkflowLiveSuite.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | StoredWorkflowLiveSuite]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    suite_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: AdvanceStoredWorkflowLiveSuiteRequest,
) -> Response[Problem | StoredWorkflowLiveSuite]:
    """Check retained results and admit at most one next case of a managed suite.

     Requires explicit real-action confirmation on every call. Checks earlier original run IDs and stops
    on failed, unavailable, outdated or nonterminal results. The next admission and its immutable link
    commit together. Never recreates missing suites, replaces runs, resumes workflows or cancels them.
    Repeat while pending to observe execution; reopening history is read-only.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):
        body (AdvanceStoredWorkflowLiveSuiteRequest): Explicit authorization to inspect existing
            identities and admit at most one remaining case. No target changes, run replacements or
            uploaded verdicts are accepted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StoredWorkflowLiveSuite]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        suite_id=suite_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    suite_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: AdvanceStoredWorkflowLiveSuiteRequest,
) -> Problem | StoredWorkflowLiveSuite | None:
    """Check retained results and admit at most one next case of a managed suite.

     Requires explicit real-action confirmation on every call. Checks earlier original run IDs and stops
    on failed, unavailable, outdated or nonterminal results. The next admission and its immutable link
    commit together. Never recreates missing suites, replaces runs, resumes workflows or cancels them.
    Repeat while pending to observe execution; reopening history is read-only.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):
        body (AdvanceStoredWorkflowLiveSuiteRequest): Explicit authorization to inspect existing
            identities and admit at most one remaining case. No target changes, run replacements or
            uploaded verdicts are accepted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StoredWorkflowLiveSuite
    """

    return sync_detailed(
        slug=slug,
        name=name,
        suite_id=suite_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    suite_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: AdvanceStoredWorkflowLiveSuiteRequest,
) -> Response[Problem | StoredWorkflowLiveSuite]:
    """Check retained results and admit at most one next case of a managed suite.

     Requires explicit real-action confirmation on every call. Checks earlier original run IDs and stops
    on failed, unavailable, outdated or nonterminal results. The next admission and its immutable link
    commit together. Never recreates missing suites, replaces runs, resumes workflows or cancels them.
    Repeat while pending to observe execution; reopening history is read-only.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):
        body (AdvanceStoredWorkflowLiveSuiteRequest): Explicit authorization to inspect existing
            identities and admit at most one remaining case. No target changes, run replacements or
            uploaded verdicts are accepted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StoredWorkflowLiveSuite]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        suite_id=suite_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    suite_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: AdvanceStoredWorkflowLiveSuiteRequest,
) -> Problem | StoredWorkflowLiveSuite | None:
    """Check retained results and admit at most one next case of a managed suite.

     Requires explicit real-action confirmation on every call. Checks earlier original run IDs and stops
    on failed, unavailable, outdated or nonterminal results. The next admission and its immutable link
    commit together. Never recreates missing suites, replaces runs, resumes workflows or cancels them.
    Repeat while pending to observe execution; reopening history is read-only.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):
        body (AdvanceStoredWorkflowLiveSuiteRequest): Explicit authorization to inspect existing
            identities and admit at most one remaining case. No target changes, run replacements or
            uploaded verdicts are accepted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StoredWorkflowLiveSuite
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            suite_id=suite_id,
            client=client,
            body=body,
        )
    ).parsed
