from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_queued_run_cancel_request import WorkflowQueuedRunCancelRequest
from ...models.workflow_queued_run_cancel_response import WorkflowQueuedRunCancelResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: WorkflowQueuedRunCancelRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/workflows/runs:cancel-queued".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowQueuedRunCancelResponse | None:
    if response.status_code == 200:
        response_200 = WorkflowQueuedRunCancelResponse.from_dict(response.json())

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
) -> Response[Problem | WorkflowQueuedRunCancelResponse]:
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
    body: WorkflowQueuedRunCancelRequest,
) -> Response[Problem | WorkflowQueuedRunCancelResponse]:
    """Cancel selected queued workflow runs that have never started.

     Atomically rechecks and cancels the selected runs that remain pending
    and have no started_at timestamp. A run claimed after preview is
    reported as already_started or not_queued and is left alone. Started
    runs and retries are never cancelled by this bulk action. Eligible
    runs transition to failed with cancelled_at set. The bounded batch is
    committed as one transaction.

    Args:
        slug (str):
        body (WorkflowQueuedRunCancelRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowQueuedRunCancelResponse]
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
    client: AuthenticatedClient | Client,
    body: WorkflowQueuedRunCancelRequest,
) -> Problem | WorkflowQueuedRunCancelResponse | None:
    """Cancel selected queued workflow runs that have never started.

     Atomically rechecks and cancels the selected runs that remain pending
    and have no started_at timestamp. A run claimed after preview is
    reported as already_started or not_queued and is left alone. Started
    runs and retries are never cancelled by this bulk action. Eligible
    runs transition to failed with cancelled_at set. The bounded batch is
    committed as one transaction.

    Args:
        slug (str):
        body (WorkflowQueuedRunCancelRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowQueuedRunCancelResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: WorkflowQueuedRunCancelRequest,
) -> Response[Problem | WorkflowQueuedRunCancelResponse]:
    """Cancel selected queued workflow runs that have never started.

     Atomically rechecks and cancels the selected runs that remain pending
    and have no started_at timestamp. A run claimed after preview is
    reported as already_started or not_queued and is left alone. Started
    runs and retries are never cancelled by this bulk action. Eligible
    runs transition to failed with cancelled_at set. The bounded batch is
    committed as one transaction.

    Args:
        slug (str):
        body (WorkflowQueuedRunCancelRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowQueuedRunCancelResponse]
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
    client: AuthenticatedClient | Client,
    body: WorkflowQueuedRunCancelRequest,
) -> Problem | WorkflowQueuedRunCancelResponse | None:
    """Cancel selected queued workflow runs that have never started.

     Atomically rechecks and cancels the selected runs that remain pending
    and have no started_at timestamp. A run claimed after preview is
    reported as already_started or not_queued and is left alone. Started
    runs and retries are never cancelled by this bulk action. Eligible
    runs transition to failed with cancelled_at set. The bounded batch is
    committed as one transaction.

    Args:
        slug (str):
        body (WorkflowQueuedRunCancelRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowQueuedRunCancelResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
