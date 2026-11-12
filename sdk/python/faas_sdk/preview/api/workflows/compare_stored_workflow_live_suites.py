from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.stored_workflow_live_suite_comparison import StoredWorkflowLiveSuiteComparison
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    suite_id: UUID,
    baseline_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/workflows/stored/{name}/live-suites/{suite_id}/compare/{baseline_id}".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
            suite_id=quote(str(suite_id), safe=""),
            baseline_id=quote(str(baseline_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | StoredWorkflowLiveSuiteComparison | None:
    if response.status_code == 200:
        response_200 = StoredWorkflowLiveSuiteComparison.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

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
) -> Response[Problem | StoredWorkflowLiveSuiteComparison]:
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
    baseline_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | StoredWorkflowLiveSuiteComparison]:
    """Compare recorded results in one read-only snapshot without executing or checking runs.

     Cases match by name and require equal frozen definition/input/assertion hashes for regression or
    recovery classification. Historical target drift alone does not invalidate a report. Pending,
    missing, unchecked and stale reports are unknown. Added and removed refer to the suite selection. No
    release approval or CI gate is implied.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):
        baseline_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StoredWorkflowLiveSuiteComparison]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        suite_id=suite_id,
        baseline_id=baseline_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    suite_id: UUID,
    baseline_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | StoredWorkflowLiveSuiteComparison | None:
    """Compare recorded results in one read-only snapshot without executing or checking runs.

     Cases match by name and require equal frozen definition/input/assertion hashes for regression or
    recovery classification. Historical target drift alone does not invalidate a report. Pending,
    missing, unchecked and stale reports are unknown. Added and removed refer to the suite selection. No
    release approval or CI gate is implied.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):
        baseline_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StoredWorkflowLiveSuiteComparison
    """

    return sync_detailed(
        slug=slug,
        name=name,
        suite_id=suite_id,
        baseline_id=baseline_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    suite_id: UUID,
    baseline_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | StoredWorkflowLiveSuiteComparison]:
    """Compare recorded results in one read-only snapshot without executing or checking runs.

     Cases match by name and require equal frozen definition/input/assertion hashes for regression or
    recovery classification. Historical target drift alone does not invalidate a report. Pending,
    missing, unchecked and stale reports are unknown. Added and removed refer to the suite selection. No
    release approval or CI gate is implied.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):
        baseline_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StoredWorkflowLiveSuiteComparison]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        suite_id=suite_id,
        baseline_id=baseline_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    suite_id: UUID,
    baseline_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | StoredWorkflowLiveSuiteComparison | None:
    """Compare recorded results in one read-only snapshot without executing or checking runs.

     Cases match by name and require equal frozen definition/input/assertion hashes for regression or
    recovery classification. Historical target drift alone does not invalidate a report. Pending,
    missing, unchecked and stale reports are unknown. Added and removed refer to the suite selection. No
    release approval or CI gate is implied.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):
        baseline_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StoredWorkflowLiveSuiteComparison
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            suite_id=suite_id,
            baseline_id=baseline_id,
            client=client,
        )
    ).parsed
