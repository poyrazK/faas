from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_run_response import WorkflowRunResponse
from ...types import Response


def _get_kwargs(
    id: UUID,
    step: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/workflows/runs/{id}/steps/{step}/retry".format(
            id=quote(str(id), safe=""),
            step=quote(str(step), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowRunResponse | None:
    if response.status_code == 200:
        response_200 = WorkflowRunResponse.from_dict(response.json())

        return response_200

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
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[Problem | WorkflowRunResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    step: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | WorkflowRunResponse]:
    """Retry one failed or dead HTTP step in its existing workflow run.

     Requeues the same run and preserves the failed step's persisted input
    and attempt history. Only terminal failed or dead HTTP steps are eligible. The
    request conflicts if another step is active, failed, or dead, a downstream
    step already succeeded, or the run was cancelled. Skipped dependent
    steps are reopened so ordinary DAG evaluation can continue. Each manual
    retry grants one new dispatch and does not reset the manifest's
    automatic retry budget.

    Args:
        id (UUID):
        step (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRunResponse]
    """

    kwargs = _get_kwargs(
        id=id,
        step=step,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    step: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | WorkflowRunResponse | None:
    """Retry one failed or dead HTTP step in its existing workflow run.

     Requeues the same run and preserves the failed step's persisted input
    and attempt history. Only terminal failed or dead HTTP steps are eligible. The
    request conflicts if another step is active, failed, or dead, a downstream
    step already succeeded, or the run was cancelled. Skipped dependent
    steps are reopened so ordinary DAG evaluation can continue. Each manual
    retry grants one new dispatch and does not reset the manifest's
    automatic retry budget.

    Args:
        id (UUID):
        step (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRunResponse
    """

    return sync_detailed(
        id=id,
        step=step,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    step: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | WorkflowRunResponse]:
    """Retry one failed or dead HTTP step in its existing workflow run.

     Requeues the same run and preserves the failed step's persisted input
    and attempt history. Only terminal failed or dead HTTP steps are eligible. The
    request conflicts if another step is active, failed, or dead, a downstream
    step already succeeded, or the run was cancelled. Skipped dependent
    steps are reopened so ordinary DAG evaluation can continue. Each manual
    retry grants one new dispatch and does not reset the manifest's
    automatic retry budget.

    Args:
        id (UUID):
        step (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRunResponse]
    """

    kwargs = _get_kwargs(
        id=id,
        step=step,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    step: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | WorkflowRunResponse | None:
    """Retry one failed or dead HTTP step in its existing workflow run.

     Requeues the same run and preserves the failed step's persisted input
    and attempt history. Only terminal failed or dead HTTP steps are eligible. The
    request conflicts if another step is active, failed, or dead, a downstream
    step already succeeded, or the run was cancelled. Skipped dependent
    steps are reopened so ordinary DAG evaluation can continue. Each manual
    retry grants one new dispatch and does not reset the manifest's
    automatic retry budget.

    Args:
        id (UUID):
        step (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRunResponse
    """

    return (
        await asyncio_detailed(
            id=id,
            step=step,
            client=client,
        )
    ).parsed
