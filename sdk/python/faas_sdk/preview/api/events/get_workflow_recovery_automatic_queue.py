from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_recovery_automatic_queue import WorkflowRecoveryAutomaticQueue
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/recovery-policy/queue".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowRecoveryAutomaticQueue | None:
    if response.status_code == 200:
        response_200 = WorkflowRecoveryAutomaticQueue.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkflowRecoveryAutomaticQueue]:
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
) -> Response[Problem | WorkflowRecoveryAutomaticQueue]:
    """Inspect queued automatic retries

     Returns a consistent app-scoped snapshot of current pending intents, unresolved applied children
    across policy versions, and execution charges across all versions. Includes at most 100 entries in
    review order. Reasons reflect the last worker evaluation and are informational; unknown evidence
    never grants execution authority.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRecoveryAutomaticQueue]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
) -> Problem | WorkflowRecoveryAutomaticQueue | None:
    """Inspect queued automatic retries

     Returns a consistent app-scoped snapshot of current pending intents, unresolved applied children
    across policy versions, and execution charges across all versions. Includes at most 100 entries in
    review order. Reasons reflect the last worker evaluation and are informational; unknown evidence
    never grants execution authority.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRecoveryAutomaticQueue
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | WorkflowRecoveryAutomaticQueue]:
    """Inspect queued automatic retries

     Returns a consistent app-scoped snapshot of current pending intents, unresolved applied children
    across policy versions, and execution charges across all versions. Includes at most 100 entries in
    review order. Reasons reflect the last worker evaluation and are informational; unknown evidence
    never grants execution authority.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRecoveryAutomaticQueue]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
) -> Problem | WorkflowRecoveryAutomaticQueue | None:
    """Inspect queued automatic retries

     Returns a consistent app-scoped snapshot of current pending intents, unresolved applied children
    across policy versions, and execution charges across all versions. Includes at most 100 entries in
    review order. Reasons reflect the last worker evaluation and are informational; unknown evidence
    never grants execution authority.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRecoveryAutomaticQueue
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
